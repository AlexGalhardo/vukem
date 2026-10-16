package vukem

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/router"
)

// boost:mcp is a Model Context Protocol server over stdio (JSON-RPC 2.0, one
// message per line) that gives coding agents read-only tools about the app.
// `vukem boost:install` registers it in the project's agent configs.

const (
	mcpLatestVersion = "2025-06-18"
	boostDocsURL     = "https://vukem.vercel.app/llms-full.txt"
	boostMaxRows     = 100
)

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

func boostMCPCommand() Command {
	return CommandFunc{
		CommandName: "boost:mcp",
		Desc:        "Run the Vukem Boost MCP server on stdio (for coding agents)",
		Fn: func(ctx context.Context, app *App, _ []string) error {
			return newBoostServer(app).serve(ctx)
		},
	}
}

type boostServer struct {
	app     *App
	docsURL string
	http    *http.Client
	tools   []boostTool
}

type boostTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	run         func(ctx context.Context, args json.RawMessage) (any, error)
}

func newBoostServer(app *App) *boostServer {
	s := &boostServer{app: app, docsURL: boostDocsURL, http: &http.Client{Timeout: 10 * time.Second}}

	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}

		return m
	}

	s.tools = []boostTool{
		{
			Name:        "application_info",
			Description: "Vukem, Go and module versions, environment, database dialect and route count.",
			InputSchema: obj(map[string]any{}),
			run:         s.applicationInfo,
		},
		{
			Name:        "database_query",
			Description: "Run one read-only SQL query (SELECT/WITH/EXPLAIN) and return up to 100 rows.",
			InputSchema: obj(map[string]any{"query": str("a single read-only SQL statement")}, "query"),
			run:         s.databaseQuery,
		},
		{
			Name:        "database_schema",
			Description: "Tables and columns (name, type, nullability, primary key) of the default database.",
			InputSchema: obj(map[string]any{"table": str("only this table")}),
			run:         s.databaseSchema,
		},
		{
			Name:        "get_absolute_url",
			Description: "Absolute URL for a named route (APP_URL + path).",
			InputSchema: obj(map[string]any{
				"name":   str("route name, e.g. posts.show"),
				"params": map[string]any{"type": "object", "description": "path parameters"},
			}, "name"),
			run: s.absoluteURL,
		},
		{
			Name:        "list_commands",
			Description: "App commands (migrate, queue:work, user commands) with descriptions.",
			InputSchema: obj(map[string]any{}),
			run:         s.listCommands,
		},
		{
			Name:        "list_routes",
			Description: "Registered routes: method, path, name and middleware, optionally filtered.",
			InputSchema: obj(map[string]any{
				"method": str(
					"HTTP method",
				),
				"path": str("substring of the path"),
				"name": str("substring of the name"),
			}),
			run: s.listRoutes,
		},
		{
			Name:        "search_docs",
			Description: "Search the Vukem documentation; returns the best matching sections.",
			InputSchema: obj(map[string]any{"query": str("words to look for")}, "query"),
			run:         s.searchDocs,
		},
	}

	return s
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *boostServer) serve(ctx context.Context) error {
	sc := bufio.NewScanner(s.app.stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)

	enc := json.NewEncoder(s.app.stdout)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			if err := reply(enc, json.RawMessage("null"), nil, &rpcError{-32700, "parse error"}); err != nil {
				return err
			}

			continue
		}

		if len(msg.ID) == 0 { // notification
			continue
		}

		result, rerr := s.handle(ctx, msg)
		if err := reply(enc, msg.ID, result, rerr); err != nil {
			return err
		}
	}

	return sc.Err()
}

func reply(enc *json.Encoder, id json.RawMessage, result any, rerr *rpcError) error {
	out := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		out["error"] = rerr
	} else {
		out["result"] = result
	}

	return enc.Encode(out)
}

func (s *boostServer) handle(ctx context.Context, msg rpcMessage) (any, *rpcError) {
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}

		_ = json.Unmarshal(msg.Params, &p)

		v := mcpLatestVersion
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}

		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "vukem-boost", "version": vukemVersion()},
			"instructions": "Tools for a Vukem (Go) application. Read AGENTS.md for the project conventions; " +
				"use search_docs before guessing framework APIs.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}

		i := slices.IndexFunc(s.tools, func(t boostTool) bool { return t.Name == p.Name })
		if i < 0 {
			return nil, &rpcError{-32602, "unknown tool " + p.Name}
		}

		if len(p.Arguments) == 0 {
			p.Arguments = json.RawMessage("{}")
		}

		out, err := s.tools[i].run(ctx, p.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}

		text, ok := out.(string)
		if !ok {
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return toolResult(err.Error(), true), nil
			}

			text = string(b)
		}

		return toolResult(text, false), nil
	default:
		return nil, &rpcError{-32601, "method not found: " + msg.Method}
	}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}

func vukemVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	if bi.Main.Path == "github.com/AlexGalhardo/vukem" {
		return bi.Main.Version
	}

	for _, d := range bi.Deps {
		if d.Path == "github.com/AlexGalhardo/vukem" {
			return d.Version
		}
	}

	return "unknown"
}

func (s *boostServer) applicationInfo(context.Context, json.RawMessage) (any, error) {
	info := map[string]any{
		"name":        s.app.Config.Name,
		"environment": string(s.app.Config.Env),
		"debug":       s.app.Config.Debug,
		"url":         s.app.Config.URL,
		"go":          runtime.Version(),
		"vukem":       vukemVersion(),
		"routes":      len(s.app.Router.Routes()),
	}

	if db, err := s.app.DB(); err == nil {
		info["database"] = db.Dialect().Name()
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		mods := map[string]string{}
		for _, d := range bi.Deps {
			if strings.HasPrefix(d.Path, "github.com/AlexGalhardo/vukem") {
				mods[d.Path] = d.Version
			}
		}

		info["modules"] = mods
	}

	return info, nil
}

func (s *boostServer) listRoutes(_ context.Context, raw json.RawMessage) (any, error) {
	var f struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}

	if _, err := s.app.Router.Handler(); err != nil {
		return nil, err
	}

	routes := slices.DeleteFunc(s.app.Router.Routes(), func(r router.RouteInfo) bool {
		return (f.Method != "" && !strings.EqualFold(r.Method, f.Method)) ||
			!strings.Contains(r.Path, f.Path) || !strings.Contains(r.Name, f.Name)
	})

	return routes, nil
}

func (s *boostServer) listCommands(context.Context, json.RawMessage) (any, error) {
	out := map[string]string{}
	for _, c := range s.app.Commands() {
		out[c.Name()] = c.Description()
	}

	return out, nil
}

func (s *boostServer) absoluteURL(_ context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}

	if _, err := s.app.Router.Handler(); err != nil {
		return nil, err
	}

	params := make([]any, 0, 2*len(a.Params))
	for _, k := range slices.Sorted(maps.Keys(a.Params)) {
		params = append(params, k, a.Params[k])
	}

	path, err := s.app.Router.URL(a.Name, params...)
	if err != nil {
		return nil, err
	}

	return strings.TrimSuffix(s.app.Config.URL, "/") + path, nil
}

func (s *boostServer) databaseSchema(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Table string `json:"table"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}

	db, err := s.app.DB()
	if err != nil {
		return nil, err
	}

	q := `SELECT table_name, column_name, data_type, is_nullable = 'YES', false
		FROM information_schema.columns WHERE table_schema = current_schema()
		ORDER BY table_name, ordinal_position`
	switch db.Dialect().Name() {
	case "sqlite":
		q = `SELECT m.name, p.name, p.type, p."notnull" = 0, p.pk > 0
			FROM sqlite_master m JOIN pragma_table_info(m.name) p
			WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name, p.cid`
	case "mysql":
		q = `SELECT table_name, column_name, column_type, is_nullable = 'YES', column_key = 'PRI'
			FROM information_schema.columns WHERE table_schema = DATABASE()
			ORDER BY table_name, ordinal_position`
	}

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	defer rows.Close()

	type column struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Nullable bool   `json:"nullable"`
		Primary  bool   `json:"primary_key,omitempty"`
	}

	tables := map[string][]column{}

	for rows.Next() {
		var (
			table string
			c     column
		)

		if err := rows.Scan(&table, &c.Name, &c.Type, &c.Nullable, &c.Primary); err != nil {
			return nil, err
		}

		if a.Table == "" || a.Table == table {
			tables[table] = append(tables[table], c)
		}
	}

	return tables, rows.Err()
}

var (
	readOnlyStart = regexp.MustCompile(`(?i)^\s*(select|with|explain|values)\b`)
	writeKeyword  = regexp.MustCompile(
		`(?i)\b(insert|update|delete|merge|upsert|replace|drop|alter|create|truncate|attach|detach|grant|revoke|vacuum|pragma|copy|call|do|lock|set)\b`,
	)
	errNotReadOnly = errors.New("only a single read-only statement (SELECT, WITH, EXPLAIN) is allowed")
)

// databaseQuery runs one read-only statement. Defence in depth: a keyword
// filter, then a read-only connection (SQLite query_only) or transaction.
func (s *boostServer) databaseQuery(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}

	q := strings.TrimSuffix(strings.TrimSpace(a.Query), ";")
	if !readOnlyStart.MatchString(q) || strings.Contains(q, ";") || writeKeyword.MatchString(q) {
		return nil, errNotReadOnly
	}

	db, err := s.app.DB()
	if err != nil {
		return nil, err
	}

	conn, err := db.SQL().Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if db.Dialect().Name() == "sqlite" {
		if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
			return nil, err
		}

		defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA query_only = OFF") }()
	}

	query := conn.QueryContext

	// SQLite: the DSN may open IMMEDIATE (write) transactions, which
	// query_only refuses, so query the connection directly.
	if db.Dialect().Name() != "sqlite" {
		tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()

		query = tx.QueryContext
	}

	rows, err := query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var out []map[string]any

	for rows.Next() && len(out) < boostMaxRows {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))

		for i := range vals {
			ptrs[i] = &vals[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}

			row[c] = vals[i]
		}

		out = append(out, row)
	}

	return out, rows.Err()
}

// searchDocs downloads llms-full.txt (sections separated by "---") and
// returns the sections containing every query word, best first.
func (s *boostServer) searchDocs(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}

	words := strings.Fields(strings.ToLower(a.Query))
	if len(words) == 0 {
		return nil, errors.New("query is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.docsURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	res, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch docs: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch docs: %s", res.Status)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}

	type hit struct {
		text  string
		score int
	}

	var hits []hit

	for sec := range strings.SplitSeq(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n---\n") {
		lower, score := strings.ToLower(sec), 0

		for _, w := range words {
			n := strings.Count(lower, w)
			if n == 0 {
				score = 0
				break
			}

			score += n
		}

		if score > 0 {
			hits = append(hits, hit{strings.TrimSpace(sec), score})
		}
	}

	if len(hits) == 0 {
		return "No documentation section matches " + a.Query, nil
	}

	slices.SortStableFunc(hits, func(x, y hit) int { return y.score - x.score })

	var b strings.Builder

	for i, h := range hits[:min(5, len(hits))] {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}

		b.WriteString(h.text[:min(4000, len(h.text))])
	}

	return b.String(), nil
}
