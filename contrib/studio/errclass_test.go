package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type stateError struct{}

func (stateError) Error() string    { return "duplicate value S3cret" }
func (stateError) SQLState() string { return "23505" }

func duplicateKey() error {
	return mongo.WriteException{WriteErrors: []mongo.WriteError{{
		Code: 11000, Message: `E11000 dup key: { email: "S3cret@example.com" }`,
	}}}
}

func TestErrorClassNeverCarriesTheText(t *testing.T) {
	t.Parallel()

	for want, err := range map[string]error{
		"not_found":      ErrNotFound,
		"invalid_input":  inputError("S3cret"),
		"canceled":       fmt.Errorf("S3cret: %w", context.Canceled),
		"timeout":        fmt.Errorf("S3cret: %w", context.DeadlineExceeded),
		"duplicate_key":  &queryError{duplicateKey()},
		"sqlstate_23505": &queryError{stateError{}},
		"query_failed":   &queryError{errors.New("S3cret")},
		"refused":        errors.New("studio: refused"),
		"failed":         errors.New("S3cret"),
	} {
		if got := errorClass(err); got != want {
			t.Errorf("errorClass(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestWriteFailureLogsNoDatabaseText(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, true)
	err := &queryError{duplicateKey()}

	h.s.audit(t.Context(), h.mongo, "insert", "users", err)

	rec := httptest.NewRecorder()
	target := "/api/v1/sources/mongo/collections/users/documents"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, nil)
	h.s.apiWriteFail(rec, req, err)

	if out := h.logs.String(); strings.Contains(out, "S3cret") || !strings.Contains(out, "duplicate_key") {
		t.Errorf("the log must name the class and hold no value: %s", out)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if rec.Code != http.StatusInternalServerError || body.Error.Code != "query_failed" {
		t.Errorf("a database failure = %d %s, want 500 query_failed", rec.Code, rec.Body)
	}
}
