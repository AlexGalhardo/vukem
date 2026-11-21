package mcp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/z"
)

// Prompt describes a prompt template. Its arguments come from the fields of
// the struct passed to AddPrompt.
type Prompt struct {
	Name        string
	Title       string
	Description string
	Icons       []Icon
}

// PromptMessage is one message of a prompt; Role is "user" or "assistant".
type PromptMessage struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// PromptResult is the answer of a prompt function.
type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// Completion is the answer of a CompleteFunc. At most 100 values are sent.
type Completion struct {
	Values  []string
	Total   *int
	HasMore bool
}

// CompleteFunc suggests values for argument, given what the user typed and
// the arguments already resolved.
type CompleteFunc func(ctx context.Context, argument, value string, resolved map[string]string) (Completion, error)

type prompt struct {
	def      map[string]any
	args     []string
	get      func(ctx context.Context, args map[string]any) (PromptResult, error)
	complete CompleteFunc
}

// AddPrompt registers a prompt. A is a struct of string fields, one per
// prompt argument (the MCP wire format only carries strings); `validate`
// tags are enforced and `required` marks the argument required. complete may
// be nil. It panics on a bad name, a duplicate or a non-string field.
func AddPrompt[A any](s *Server, p Prompt, fn func(context.Context, A) (PromptResult, error), complete CompleteFunc) {
	if !nameRe.MatchString(p.Name) {
		panic(fmt.Sprintf("mcp: invalid prompt name %q", p.Name))
	}

	at := reflect.TypeFor[A]()
	if at.Kind() != reflect.Struct {
		panic(fmt.Sprintf("mcp: prompt %q: argument type %s is not a struct", p.Name, at))
	}

	var (
		list  []map[string]any
		names []string
	)

	for _, f := range openapi.Fields(at) {
		if f.Type.Kind() != reflect.String {
			panic(fmt.Sprintf("mcp: prompt %q: argument %q is not a string", p.Name, f.Key))
		}

		a := map[string]any{"name": f.Key, "required": f.Required}
		setIf(a, "description", f.Doc)

		list = append(list, a)
		names = append(names, f.Key)
	}

	def := map[string]any{"name": p.Name}
	setIf(def, "title", p.Title)
	setIf(def, "description", p.Description)

	if len(list) > 0 {
		def["arguments"] = list
	}

	if len(p.Icons) > 0 {
		def["icons"] = p.Icons
	}

	schema := z.For[A]().Strict()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.prompts[p.Name]; dup {
		panic(fmt.Sprintf("mcp: prompt %q registered twice", p.Name))
	}

	s.prompts[p.Name] = &prompt{
		def: def, args: names, complete: complete,
		get: func(ctx context.Context, in map[string]any) (PromptResult, error) {
			a, err := schema.Parse(in)
			if err != nil {
				if is, ok := z.AsIssues(err); ok {
					return PromptResult{}, &Error{Code: CodeInvalidParams, Message: "Invalid params: " + is.Error()}
				}

				return PromptResult{}, err
			}

			return fn(ctx, a)
		},
	}
}

func (s *Server) listPrompts(c *call) (any, *Error) {
	s.mu.RLock()
	empty := len(s.prompts) == 0
	keys := make([]string, 0, len(s.prompts))
	defs := map[string]any{}

	for k, p := range s.prompts {
		keys = append(keys, k)
		defs[k] = p.def
	}

	s.mu.RUnlock()

	if empty {
		return nil, methodNotFound(c.msg.Method)
	}

	return pageResult(s, c, "prompts", keys, func(k string) any { return defs[k] })
}

func (s *Server) getPrompt(ctx context.Context, c *call) (any, *Error) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
		inputParams
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return nil, perr
	}

	s.mu.RLock()
	pr := s.prompts[p.Name]
	s.mu.RUnlock()

	if pr == nil {
		return nil, invalidParams("unknown prompt " + quote(p.Name))
	}

	for k := range p.Arguments {
		if !slices.Contains(pr.args, k) {
			return nil, invalidParams("unknown argument " + quote(k))
		}
	}

	in := make(map[string]any, len(p.Arguments))
	for k, v := range p.Arguments {
		in[k] = v
	}

	ctx, perr := s.withInputs(ctx, c, p.inputParams, p.Name, in)
	if perr != nil {
		return nil, perr
	}

	res, err := safeRun(func() (PromptResult, error) { return pr.get(ctx, in) })
	if err != nil {
		if e, ok := errors.AsType[*Error](err); ok {
			return nil, e
		}

		return s.failure(ctx, c, "prompt "+p.Name, err)
	}

	if res.Messages == nil {
		res.Messages = []PromptMessage{}
	}

	return res, nil
}

func (s *Server) hasCompletions() bool {
	for _, p := range s.prompts {
		if p.complete != nil {
			return true
		}
	}

	for _, t := range s.templates {
		if t.complete != nil {
			return true
		}
	}

	return false
}

const maxCompletions = 100

func (s *Server) complete(ctx context.Context, c *call) (any, *Error) {
	var p struct {
		Ref struct {
			Type string `json:"type"`
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"ref"`
		Argument struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"argument"`
		Context struct {
			Arguments map[string]string `json:"arguments"`
		} `json:"context"`
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return nil, perr
	}

	s.mu.RLock()
	has := s.hasCompletions()

	var fn CompleteFunc

	switch p.Ref.Type {
	case "ref/prompt":
		if pr := s.prompts[p.Ref.Name]; pr != nil {
			fn = pr.complete
		}
	case "ref/resource":
		if t := s.templates[p.Ref.URI]; t != nil {
			fn = t.complete
		}
	}
	s.mu.RUnlock()

	if !has {
		return nil, methodNotFound(c.msg.Method)
	}

	if p.Argument.Name == "" {
		return nil, invalidParams("missing argument")
	}

	if fn == nil {
		return nil, invalidParams("unknown reference")
	}

	got, err := safeRun(func() (Completion, error) {
		return fn(ctx, p.Argument.Name, p.Argument.Value, p.Context.Arguments)
	})
	if err != nil {
		return s.failure(ctx, c, "completion", err)
	}

	if len(got.Values) > maxCompletions {
		got.Values, got.HasMore = got.Values[:maxCompletions], true
	}

	if got.Values == nil {
		got.Values = []string{}
	}

	out := map[string]any{"values": got.Values, "hasMore": got.HasMore}
	if got.Total != nil {
		out["total"] = *got.Total
	}

	return map[string]any{"completion": out}, nil
}
