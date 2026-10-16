package cli

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// boostFS holds the agent guidelines and skills written by boost:install.
//
//go:embed boost
var boostFS embed.FS

const (
	boostStart = "<!-- vukem-boost:start -->"
	boostEnd   = "<!-- vukem-boost:end -->"
)

// boostServerArgs starts the app-bound MCP server (vukem.boostMCPCommand).
var boostServerArgs = []any{"run", "./cmd/app", "boost:mcp"}

// boostInstall writes agent guidelines into AGENTS.md (between markers, so
// it can be re-run), framework skills into .claude/skills and the
// vukem-boost MCP server into .mcp.json, plus .cursor/ and .vscode/ when
// those directories exist.
func boostInstall(*flag.FlagSet) func(context.Context, *Env, []string) error {
	return func(_ context.Context, env *Env, _ []string) error {
		// Validate the JSON configs first so a broken file stops before any write.
		type mcpConfig struct {
			rel, key string
			server   map[string]any
		}

		server := map[string]any{"command": "go", "args": boostServerArgs}
		configs := []mcpConfig{{".mcp.json", "mcpServers", server}}

		if exists(env, ".cursor") {
			configs = append(configs, mcpConfig{filepath.Join(".cursor", "mcp.json"), "mcpServers", server})
		}

		if exists(env, ".vscode") {
			configs = append(configs, mcpConfig{
				filepath.Join(".vscode", "mcp.json"), "servers",
				map[string]any{"type": "stdio", "command": "go", "args": boostServerArgs},
			})
		}

		docs := make([]map[string]any, len(configs))

		for i, c := range configs {
			doc, err := readJSONObject(env.path(c.rel))
			if err != nil {
				return err
			}

			docs[i] = doc
		}

		guidelines, err := fs.ReadFile(boostFS, "boost/guidelines.md")
		if err != nil {
			return err
		}

		if err := upsertBlock(env.path("AGENTS.md"), "# AGENTS.md\n", string(guidelines)); err != nil {
			return err
		}

		if err := ensureClaudeImport(env.path("CLAUDE.md")); err != nil {
			return err
		}

		if err := writeSkills(env); err != nil {
			return err
		}

		for i, c := range configs {
			servers, _ := docs[i][c.key].(map[string]any)
			if servers == nil {
				servers = map[string]any{}
			}

			servers["vukem-boost"] = c.server
			docs[i][c.key] = servers

			b, err := json.MarshalIndent(docs[i], "", "  ")
			if err != nil {
				return err
			}

			if err := writeFile(env.path(c.rel), append(b, '\n')); err != nil {
				return err
			}
		}

		fmt.Fprintln(
			env.Stdout,
			"Vukem Boost installed: AGENTS.md guidelines, .claude/skills/vukem-*, MCP server vukem-boost.",
		)

		return nil
	}
}

func readJSONObject(p string) (map[string]any, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}

	if err != nil {
		return nil, err
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (fix or remove it first): %w", filepath.Base(p), err)
	}

	if m == nil {
		m = map[string]any{}
	}

	return m, nil
}

// upsertBlock replaces the boost block in file p, or appends it (creating
// the file with header when missing). Text outside the markers is kept.
func upsertBlock(p, header, body string) error {
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	block := boostStart + "\n" + strings.TrimSpace(body) + "\n" + boostEnd + "\n"
	s := string(b)

	start, end := strings.Index(s, boostStart), strings.Index(s, boostEnd)

	switch {
	case start >= 0 && end > start:
		rest := strings.TrimPrefix(strings.TrimPrefix(s[end+len(boostEnd):], "\r"), "\n")
		s = s[:start] + block + rest
	case s == "":
		s = header + "\n" + block
	default:
		s = strings.TrimRight(s, "\r\n") + "\n\n" + block
	}

	return writeFile(p, []byte(s))
}

func ensureClaudeImport(p string) error {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return writeFile(p, []byte("@AGENTS.md\n"))
	}

	if err != nil || bytes.Contains(b, []byte("@AGENTS.md")) {
		return err
	}

	return writeFile(p, append(append(bytes.TrimRight(b, "\r\n"), "\n\n"...), "@AGENTS.md\n"...))
}

func writeSkills(env *Env) error {
	return fs.WalkDir(boostFS, "boost/skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		b, err := fs.ReadFile(boostFS, p)
		if err != nil {
			return err
		}

		rel := filepath.Join(".claude", "skills", filepath.FromSlash(strings.TrimPrefix(p, "boost/skills/")))
		if err := os.MkdirAll(filepath.Dir(env.path(rel)), 0o750); err != nil {
			return err
		}

		return writeFile(env.path(rel), b)
	})
}
