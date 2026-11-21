package mcp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrElicitationUnsupported is returned by Elicit and Confirm when the client
// did not declare the elicitation capability, or speaks a revision without
// the input_required flow. A tool that returns it unchanged is answered with
// a MissingRequiredClientCapability error; a tool may also catch it and
// continue without asking.
var ErrElicitationUnsupported = errors.New("mcp: the client does not support elicitation")

// inputParams are the retry fields of a request that may need more input.
type inputParams struct {
	InputResponses map[string]json.RawMessage `json:"inputResponses"`
	RequestState   string                     `json:"requestState"`
}

type inputRequiredError struct{ requests map[string]any }

func (e *inputRequiredError) Error() string { return "mcp: input required" }

type capabilityError struct{}

func (e *capabilityError) Error() string { return ErrElicitationUnsupported.Error() }

func (e *capabilityError) Unwrap() error { return ErrElicitationUnsupported }

// Elicitation asks the user for input described by Schema, a flat JSON Schema
// object ({"type":"object","properties":{...}}); nil asks for nothing but an
// answer.
type Elicitation struct {
	Message string
	Schema  map[string]any
}

// ElicitResult is the user's answer: Action is "accept", "decline" or
// "cancel"; Content holds the form values of an accept.
type ElicitResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// Elicit asks the user, through the client, for input. It implements the
// input_required flow: the first time it returns an error that the function
// must return unchanged, the client collects the answer and retries the same
// request, and the function runs again from the start with key answered, so
// code before Elicit must be safe to repeat. Elicit returns
// ErrElicitationUnsupported when the client cannot be asked.
func Elicit(ctx context.Context, key string, e Elicitation) (ElicitResult, error) {
	ri, ok := RequestFrom(ctx)
	if !ok || !ri.call.modern || !supportsForm(ri.call.meta.Capabilities) {
		return ElicitResult{}, &capabilityError{}
	}

	if raw, ok := ri.inputs[key]; ok {
		var res ElicitResult
		if err := json.Unmarshal(raw, &res); err != nil ||
			(res.Action != "accept" && res.Action != "decline" && res.Action != "cancel") {
			return ElicitResult{}, &inputRequiredError{requests: map[string]any{key: elicitRequest(e)}}
		}

		return res, nil
	}

	return ElicitResult{}, &inputRequiredError{requests: map[string]any{key: elicitRequest(e)}}
}

// Confirm asks the user to approve an action, for example before a
// destructive tool runs. It returns true only when the user accepted; see
// Elicit for the retry behaviour.
func Confirm(ctx context.Context, message string) (bool, error) {
	res, err := Elicit(ctx, "confirm", Elicitation{Message: message})
	if err != nil {
		return false, err
	}

	return res.Action == "accept", nil
}

func elicitRequest(e Elicitation) map[string]any {
	schema := e.Schema
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	return map[string]any{
		"method": "elicitation/create",
		"params": map[string]any{"mode": "form", "message": e.Message, "requestedSchema": schema},
	}
}

// supportsForm reports whether the declared client capabilities include
// form elicitation. An empty elicitation object means form, as in the
// 2025-06-18 revision.
func supportsForm(caps json.RawMessage) bool {
	var c ClientCapabilities
	if json.Unmarshal(caps, &c) != nil || c.Elicitation == nil {
		return false
	}

	if len(c.Elicitation) == 0 {
		return true
	}

	_, form := c.Elicitation["form"]

	return form
}

// state is the integrity-protected payload of a requestState.
type state struct {
	Method  string `json:"m"`
	Key     string `json:"k"`
	Digest  string `json:"h"`
	Subject string `json:"s"`
	Expires int64  `json:"e"`
}

func digest(args any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return "unmarshalable" // never equals a real digest, so the state is refused
	}

	sum := sha256.Sum256(b)

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Server) sign(payload []byte) string {
	m := hmac.New(sha256.New, s.cfg.StateKey)
	m.Write(payload)

	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) sealState(c *call, key string, args any) string {
	st := state{Method: c.msg.Method, Key: key, Digest: digest(args), Expires: time.Now().Add(s.cfg.StateTTL).Unix()}
	if c.token != nil {
		st.Subject = c.token.Subject
	}

	b, err := json.Marshal(st)
	if err != nil {
		return ""
	}

	return s.sign(b)
}

// openState verifies a requestState against the request that carries it:
// signature, expiry, method, target, arguments and caller.
func (s *Server) openState(c *call, token, key string, args any) bool {
	body, mac, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}

	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return false
	}

	want, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil {
		return false
	}

	m := hmac.New(sha256.New, s.cfg.StateKey)
	m.Write(payload)

	if !hmac.Equal(m.Sum(nil), want) {
		return false
	}

	var st state
	if json.Unmarshal(payload, &st) != nil || time.Now().Unix() > st.Expires {
		return false
	}

	subject := ""
	if c.token != nil {
		subject = c.token.Subject
	}

	return st.Method == c.msg.Method && st.Key == key && st.Digest == digest(args) && st.Subject == subject
}

// withInputs attaches the answers of a retry to the context, but only when the
// requestState proves they answer a question this server asked this caller
// for these exact arguments. Anything else is dropped, so the function asks
// again.
func (s *Server) withInputs(
	ctx context.Context,
	c *call,
	p inputParams,
	key string,
	args any,
) (context.Context, *Error) {
	c.stateKey, c.stateArgs = key, args

	if p.RequestState == "" {
		return ctx, nil
	}

	if !s.openState(c, p.RequestState, key, args) {
		return ctx, invalidParams("invalid requestState")
	}

	if ri, ok := RequestFrom(ctx); ok {
		ri.inputs = p.InputResponses
	}

	return ctx, nil
}

func (s *Server) inputRequired(c *call, e *inputRequiredError) (any, *Error) {
	return map[string]any{
		"resultType":    "input_required",
		"inputRequests": e.requests,
		"requestState":  s.sealState(c, c.stateKey, c.stateArgs),
	}, nil
}
