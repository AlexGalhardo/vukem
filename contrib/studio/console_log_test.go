package studio

import (
	"net/http"
	"strings"
	"testing"
)

func TestSecurityConsoleErrorsNeverLogWhatWasTyped(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, true)

	for path, body := range map[string]map[string]string{
		"/api/v1/sources/mongo/find":      {"collection": "users", "filter": `{"token": S3cret-typed}`},
		"/api/v1/sources/mongo/aggregate": {"collection": "users", "pipeline": `[{"$match": S3cret-typed}]`},
		"/api/v1/sources/redis/command":   {"line": `S3cret-typed "x`},
	} {
		rec := apiDo(h.s, h.login, "POST", path, apiJSON(t, body))
		if rec.Code == http.StatusOK || rec.Code == http.StatusNotFound {
			t.Fatalf("%s = %d %s, want a refused statement", path, rec.Code, rec.Body)
		}
	}

	if out := h.logs.String(); strings.Contains(out, "S3cret") {
		t.Errorf("a typed value reached the log: %s", out)
	}
}

func TestCSVCellNeutralizesFormulasBehindControlCharacters(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"\t=1+1", "\r=1+1"} {
		if got := csvCell(in); got != "'"+in {
			t.Errorf("csvCell(%q) = %q", in, got)
		}
	}

	if got := csvCell("plain\t=1"); got != "plain\t=1" {
		t.Errorf("csvCell changed a plain cell: %q", got)
	}
}
