package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/config"
)

// doctorCheck is one project fact. Detail and Fix never carry env values,
// keys, or database credentials.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func goRuntime() string { return runtime.Version() }

func formatCheck(c doctorCheck) string {
	if c.Fix != "" && c.Status != "ok" {
		return fmt.Sprintf("%s: %s; %s\n", c.Name, c.Detail, c.Fix)
	}

	return fmt.Sprintf("%s: %s\n", c.Name, c.Detail)
}

func projectChecks(ctx context.Context, env *Env) []doctorCheck {
	vals, envErr := dotEnvMap(env)

	return []doctorCheck{
		checkGo(ctx, env),
		checkBun(env),
		checkLockfile(env),
		checkAppKey(vals, envErr),
		checkDatabase(env, vals),
		checkPort(vals),
		checkProxy(vals),
		checkVite(env, vals),
	}
}

func dotEnvMap(env *Env) (map[string]string, error) {
	f, err := os.Open(env.path(".env"))
	if errorsNotExist(err) {
		return map[string]string{}, nil
	}

	if err != nil {
		return nil, err
	}

	defer f.Close()

	entries, err := config.ParseEnv(f)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(entries))
	for _, e := range entries {
		out[e.Key] = e.Value
	}

	return out, nil
}

func errorsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func checkGo(ctx context.Context, env *Env) doctorCheck {
	_, err := exec.LookPath("go")
	if err != nil {
		status := "warn"
		if exists(env, "go.mod") {
			status = "fail"
		}

		return doctorCheck{
			Name: "go", Status: status, Detail: "go is not installed", Fix: "install Go from https://go.dev/dl/",
		}
	}

	out, err := exec.CommandContext(ctx, "go", "version").Output()
	if err != nil {
		return doctorCheck{Name: "go", Status: "warn", Detail: "go is installed but go version failed"}
	}

	return doctorCheck{Name: "go", Status: "ok", Detail: strings.TrimSpace(string(out))}
}

func checkBun(env *Env) doctorCheck {
	used := exists(env, "bun.lock") || exists(env, "bun.lockb")
	if !used {
		return doctorCheck{Name: "bun", Status: "ok", Detail: "not used"}
	}

	if _, err := exec.LookPath("bun"); err != nil {
		return doctorCheck{
			Name: "bun", Status: "warn", Detail: "bun.lock is present but bun is not installed",
			Fix: "install Bun or switch the asset stage to npm",
		}
	}

	return doctorCheck{Name: "bun", Status: "ok", Detail: "installed"}
}

func checkLockfile(env *Env) doctorCheck {
	if !exists(env, "go.mod") {
		return doctorCheck{Name: "lockfile", Status: "warn", Detail: "no go.mod", Fix: "run vukem new in the project"}
	}

	var found []string
	for _, name := range []string{"go.sum", "package-lock.json", "bun.lock"} {
		if exists(env, name) {
			found = append(found, name)
		}
	}

	if !exists(env, "go.sum") {
		return doctorCheck{Name: "lockfile", Status: "warn", Detail: "go.sum is missing", Fix: "run go mod tidy"}
	}

	return doctorCheck{Name: "lockfile", Status: "ok", Detail: strings.Join(found, ", ")}
}

func checkAppKey(vals map[string]string, envErr error) doctorCheck {
	if envErr != nil {
		return doctorCheck{Name: "app_key", Status: "fail", Detail: ".env cannot be parsed", Fix: "fix .env syntax"}
	}

	raw := strings.TrimSpace(vals["APP_KEY"])
	if raw == "" {
		return doctorCheck{
			Name: "app_key", Status: "fail", Detail: "missing", Fix: "run vukem key:generate",
		}
	}

	if _, err := config.ParseKey(config.NewSecret(raw)); err != nil {
		return doctorCheck{
			Name: "app_key", Status: "fail", Detail: "invalid", Fix: "run vukem key:rotate",
		}
	}

	return doctorCheck{Name: "app_key", Status: "ok", Detail: "set"}
}

func checkDatabase(env *Env, vals map[string]string) doctorCheck {
	raw := strings.TrimSpace(vals["DATABASE_URL"])
	if raw == "" {
		raw = "file:storage/database.sqlite"
	}

	if raw == ":memory:" || strings.Contains(raw, ":memory:") {
		return doctorCheck{Name: "database", Status: "ok", Detail: "in-memory sqlite"}
	}

	path, sqlite := sqliteFile(raw)
	if !sqlite {
		return doctorCheck{Name: "database", Status: "ok", Detail: "remote database; credentials are not shown"}
	}

	if path == "" {
		return doctorCheck{Name: "database", Status: "fail", Detail: "sqlite path is empty", Fix: "set DATABASE_URL"}
	}

	if !filepath.IsAbs(path) {
		path = env.path(path)
	}

	dir := filepath.Dir(path)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return doctorCheck{
			Name: "database", Status: "fail", Detail: "sqlite directory is missing",
			Fix: "create the directory before migrate",
		}
	}

	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return doctorCheck{
			Name: "database", Status: "fail", Detail: "sqlite directory is not writable",
			Fix: "fix the directory owner before migrate or backup",
		}
	}

	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)

	return doctorCheck{Name: "database", Status: "ok", Detail: "sqlite directory is writable"}
}

func sqliteFile(raw string) (string, bool) {
	switch {
	case strings.HasPrefix(raw, "file:"):
		raw = strings.TrimPrefix(raw, "file:")
	case strings.HasPrefix(raw, "sqlite:"):
		raw = strings.TrimPrefix(raw, "sqlite:")
	default:
		return "", false
	}

	if i := strings.IndexByte(raw, '?'); i >= 0 {
		raw = raw[:i]
	}

	return raw, true
}

func checkPort(vals map[string]string) doctorCheck {
	p := strings.TrimSpace(vals["PORT"])
	if p == "" {
		return doctorCheck{Name: "port", Status: "ok", Detail: "unset"}
	}

	n, err := strconv.Atoi(p)
	if err != nil || n < 0 || n > 65535 {
		return doctorCheck{Name: "port", Status: "fail", Detail: "PORT is not a TCP port", Fix: "set PORT to 0-65535"}
	}

	return doctorCheck{Name: "port", Status: "ok", Detail: p}
}

func checkProxy(vals map[string]string) doctorCheck {
	forwarded := strings.TrimSpace(vals["TRUST_FORWARDED_PROTO"]) != "" ||
		strings.TrimSpace(vals["CLIENT_IP_HEADER"]) != ""
	if strings.TrimSpace(vals["TRUSTED_PROXIES"]) == "" {
		if forwarded {
			return doctorCheck{
				Name: "proxy", Status: "fail", Detail: "forwarded headers are set without TRUSTED_PROXIES",
				Fix: "set TRUSTED_PROXIES or remove the header settings",
			}
		}

		return doctorCheck{Name: "proxy", Status: "ok", Detail: "unset"}
	}

	return doctorCheck{Name: "proxy", Status: "ok", Detail: "set"}
}

func checkVite(env *Env, vals map[string]string) doctorCheck {
	manifest := env.path(filepath.Join("resources", "public", "build", ".vite", "manifest.json"))
	b, err := os.ReadFile(manifest)
	if err != nil {
		status := "warn"
		if vals["APP_ENV"] == "production" {
			status = "fail"
		}

		return doctorCheck{
			Name: "vite", Status: status, Detail: "manifest is missing", Fix: "run vukem build before production",
		}
	}

	if !json.Valid(b) {
		return doctorCheck{Name: "vite", Status: "fail", Detail: "manifest is not valid JSON", Fix: "run vukem build"}
	}

	return doctorCheck{Name: "vite", Status: "ok", Detail: "manifest is present"}
}
