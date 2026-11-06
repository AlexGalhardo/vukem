package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// doctorTool is an external tool the generated project relies on outside
// the Go toolchain. Core lists only tools every starter can use. An app
// adds its own in .vukem/doctor.json; LilyPond and FFmpeg stay out of core.
type doctorTool struct {
	name, versionArg, why, install string
}

func doctorTools() []doctorTool {
	return []doctorTool{
		{
			name:       "betterleaks",
			versionArg: "version",
			why:        "secret scanning in the pre-commit hook and npm run secrets:scan",
			install:    "https://github.com/betterleaks/betterleaks/releases",
		},
	}
}

type toolLine struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Install string `json:"install,omitempty"`
	Path    string `json:"path,omitempty"`
}

func (t toolLine) String() string {
	switch t.Status {
	case "missing":
		return fmt.Sprintf("%s: not installed (%s); install it from %s\n", t.Name, t.Detail, t.Install)
	case "unknown":
		return fmt.Sprintf("%s: installed at %s, version unknown (%s)\n", t.Name, t.Path, t.Detail)
	default:
		return fmt.Sprintf("%s: %s (%s)\n", t.Name, t.Detail, t.Path)
	}
}

func inspectTool(
	ctx context.Context,
	t doctorTool,
	lookPath func(string) (string, error),
	run func(context.Context, string, string) (string, error),
) toolLine {
	bin, err := lookPath(t.name)
	if err != nil {
		return toolLine{Name: t.name, Status: "missing", Detail: t.why, Install: t.install}
	}

	arg := t.versionArg
	if arg == "" {
		arg = "version"
	}

	out, err := run(ctx, bin, arg)
	if err != nil {
		return toolLine{Name: t.name, Status: "unknown", Detail: err.Error(), Path: bin}
	}

	return toolLine{Name: t.name, Status: "installed", Detail: strings.TrimSpace(out), Path: bin}
}

// doctor reports which optional tools are installed. A missing tool is not
// an error: the app may not use it, and CI still runs the scan itself.
func doctor(
	ctx context.Context,
	env *Env,
	lookPath func(string) (string, error),
	run func(context.Context, string, string) (string, error),
) {
	for _, t := range doctorTools() {
		fmt.Fprint(env.Stdout, inspectTool(ctx, t, lookPath, run).String())
	}
}

// doctorCmd reports optional tools and project checks. --json prints one
// report. --strict fails when a project check failed. A missing optional
// tool never fails, including in --strict.
func doctorCmd(set *flag.FlagSet) func(ctx context.Context, env *Env, args []string) error {
	asJSON := set.Bool("json", false, "print one JSON report")
	strict := set.Bool("strict", false, "exit non-zero when a project check fails")

	return func(ctx context.Context, env *Env, args []string) error {
		if len(args) > 0 {
			return usagef("doctor takes no arguments")
		}

		return runDoctor(ctx, env, *asJSON, *strict)
	}
}

func runDoctor(ctx context.Context, env *Env, asJSON, strict bool) error {
	look := exec.LookPath
	run := func(ctx context.Context, bin, arg string) (string, error) {
		out, err := exec.CommandContext(ctx, bin, arg).Output()
		return string(out), err
	}

	tools := make([]toolLine, 0, 2)
	for _, t := range doctorTools() {
		tools = append(tools, inspectTool(ctx, t, look, run))
	}

	extra, cfgErr := loadExtraTools(env)
	for _, t := range extra {
		tools = append(tools, inspectTool(ctx, t, look, run))
	}

	checks := projectChecks(ctx, env)
	if cfgErr != nil {
		checks = append(checks, doctorCheck{
			Name: "doctor_config", Status: "fail", Detail: ".vukem/doctor.json is not valid JSON",
			Fix: "fix the file or remove it",
		})
	}

	if err := writeDoctor(env, asJSON, tools, checks); err != nil {
		return err
	}

	if !strict {
		return nil
	}

	n := 0
	for _, c := range checks {
		if c.Status == "fail" {
			n++
		}
	}

	if n > 0 {
		return fmt.Errorf("doctor: %d project checks failed", n)
	}

	return nil
}

func writeDoctor(env *Env, asJSON bool, tools []toolLine, checks []doctorCheck) error {
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(struct {
			CLI     string        `json:"cli"`
			Runtime string        `json:"runtime"`
			Tools   []toolLine    `json:"tools"`
			Checks  []doctorCheck `json:"checks"`
		}{CLI: version(env), Runtime: goRuntime(), Tools: tools, Checks: checks})
	}

	for _, t := range tools {
		fmt.Fprint(env.Stdout, t.String())
	}

	fmt.Fprintf(env.Stdout, "cli: %s\nruntime: %s\n", version(env), goRuntime())

	for _, c := range checks {
		fmt.Fprint(env.Stdout, formatCheck(c))
	}

	return nil
}

func loadExtraTools(env *Env) ([]doctorTool, error) {
	b, err := os.ReadFile(env.path(".vukem/doctor.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var file struct {
		Tools []struct {
			Name    string `json:"name"`
			Why     string `json:"why"`
			Install string `json:"install"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, err
	}

	out := make([]doctorTool, 0, len(file.Tools))
	for _, t := range file.Tools {
		if t.Name == "" {
			return nil, errors.New("doctor: tool name is empty")
		}

		out = append(out, doctorTool{name: t.Name, why: t.Why, install: t.Install})
	}

	return out, nil
}
