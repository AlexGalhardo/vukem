package export_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/export"
)

type user struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func seq(us ...user) iter.Seq2[user, error] {
	return func(yield func(user, error) bool) {
		for _, u := range us {
			if !yield(u, nil) {
				return
			}
		}
	}
}

// failing yields the users, then err.
func failing(err error, us ...user) iter.Seq2[user, error] {
	return func(yield func(user, error) bool) {
		for _, u := range us {
			if !yield(u, nil) {
				return
			}
		}

		yield(user{}, err)
	}
}

func record(u user) []string { return []string{strconv.Itoa(u.ID), u.Name} }

func TestFormats(t *testing.T) {
	t.Parallel()

	us := []user{{1, "Ana"}, {2, "Bia, \"B\""}}

	tests := []struct {
		name     string
		serve    func(w http.ResponseWriter, r *http.Request) error
		wantType string
		wantDisp string
		wantBody string
	}{
		{
			name: "csv",
			serve: func(w http.ResponseWriter, r *http.Request) error {
				return export.CSV(w, r, "users.csv", []string{"id", "name"}, seq(us...), record)
			},
			wantType: "text/csv; charset=utf-8",
			wantDisp: `attachment; filename="users.csv"`,
			wantBody: "id,name\n1,Ana\n2,\"Bia, \"\"B\"\"\"\n",
		},
		{
			name: "csv empty keeps header",
			serve: func(w http.ResponseWriter, r *http.Request) error {
				return export.CSV(w, r, "", []string{"id", "name"}, seq(), record)
			},
			wantType: "text/csv; charset=utf-8",
			wantDisp: `attachment; filename="export.csv"`,
			wantBody: "id,name\n",
		},
		{
			name: "csv bom",
			serve: func(w http.ResponseWriter, r *http.Request) error {
				return export.CSV(w, r, "u.csv", []string{"nome"}, seq(us[0]), func(u user) []string {
					return []string{u.Name}
				}, export.WithBOM())
			},
			wantType: "text/csv; charset=utf-8",
			wantDisp: `attachment; filename="u.csv"`,
			wantBody: "\ufeffnome\nAna\n",
		},
		{
			name: "json",
			serve: func(w http.ResponseWriter, r *http.Request) error {
				return export.JSON(w, r, "users.json", seq(us...))
			},
			wantType: "application/json; charset=utf-8",
			wantDisp: `attachment; filename="users.json"`,
			wantBody: `[{"id":1,"name":"Ana"},{"id":2,"name":"Bia, \"B\""}]` + "\n",
		},
		{
			name: "json empty",
			serve: func(w http.ResponseWriter, r *http.Request) error {
				return export.JSON(w, r, "", seq())
			},
			wantType: "application/json; charset=utf-8",
			wantDisp: `attachment; filename="export.json"`,
			wantBody: "[]\n",
		},
		{
			name: "ndjson",
			serve: func(w http.ResponseWriter, r *http.Request) error {
				return export.NDJSON(w, r, "users.ndjson", seq(us...))
			},
			wantType: "application/x-ndjson",
			wantDisp: `attachment; filename="users.ndjson"`,
			wantBody: `{"id":1,"name":"Ana"}` + "\n" + `{"id":2,"name":"Bia, \"B\""}` + "\n",
		},
		{
			name: "csv writer sections",
			serve: func(w http.ResponseWriter, r *http.Request) error {
				return export.WriteCSV(w, r, "audit.csv", func(cw *export.CSVWriter) error {
					for _, rec := range [][]string{{"record", "id"}, {"raffle", "7"}, {}, {"record", "n"}} {
						if err := cw.Write(rec); err != nil {
							return err
						}
					}

					return nil
				})
			},
			wantType: "text/csv; charset=utf-8",
			wantDisp: `attachment; filename="audit.csv"`,
			wantBody: "record,id\nraffle,7\n\nrecord,n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/export", nil)

			if err := tt.serve(rec, req); err != nil {
				t.Fatalf("serve() = %v, want nil", err)
			}

			got := map[string]string{
				"status":      http.StatusText(rec.Code),
				"type":        rec.Header().Get("Content-Type"),
				"disposition": rec.Header().Get("Content-Disposition"),
				"cache":       rec.Header().Get("Cache-Control"),
				"nosniff":     rec.Header().Get("X-Content-Type-Options"),
				"body":        rec.Body.String(),
			}
			want := map[string]string{
				"status":      "OK",
				"type":        tt.wantType,
				"disposition": tt.wantDisp,
				"cache":       "no-store",
				"nosniff":     "nosniff",
				"body":        tt.wantBody,
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("response mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestErrorBeforeFirstRowSendsNothing checks that a query that fails before
// any row leaves the response untouched, so the error handler can still
// render a proper error page.
func TestErrorBeforeFirstRowSendsNothing(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	serves := map[string]func(w http.ResponseWriter, r *http.Request) error{
		"csv": func(w http.ResponseWriter, r *http.Request) error {
			return export.CSV(w, r, "u.csv", []string{"id"}, failing(boom), record)
		},
		"json": func(w http.ResponseWriter, r *http.Request) error {
			return export.JSON(w, r, "u.json", failing(boom))
		},
		"ndjson": func(w http.ResponseWriter, r *http.Request) error {
			return export.NDJSON(w, r, "u.ndjson", failing(boom))
		},
		"csv writer": func(w http.ResponseWriter, r *http.Request) error {
			return export.WriteCSV(w, r, "u.csv", func(*export.CSVWriter) error { return boom })
		},
	}

	for name, serve := range serves {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

			if err := serve(rec, req); !errors.Is(err, boom) {
				t.Fatalf("serve() = %v, want %v", err, boom)
			}

			if rec.Flushed || rec.Body.Len() != 0 || len(rec.Header()) != 0 {
				t.Errorf("response written: headers %v, body %q", rec.Header(), rec.Body.String())
			}
		})
	}
}

// TestErrorMidStreamAbortsConnection checks that a failure after the first
// byte aborts the response, so the client sees a failed download instead
// of a truncated file that looks complete.
func TestErrorMidStreamAbortsConnection(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	serves := map[string]func(w http.ResponseWriter, r *http.Request) error{
		"csv": func(w http.ResponseWriter, r *http.Request) error {
			return export.CSV(w, r, "u.csv", []string{"id", "name"}, failing(boom, user{1, "Ana"}), record,
				export.FlushEvery(1))
		},
		"json": func(w http.ResponseWriter, r *http.Request) error {
			return export.JSON(w, r, "u.json", failing(boom, user{1, "Ana"}), export.FlushEvery(1))
		},
		"ndjson": func(w http.ResponseWriter, r *http.Request) error {
			return export.NDJSON(w, r, "u.ndjson", failing(boom, user{1, "Ana"}), export.FlushEvery(1))
		},
	}

	for name, serve := range serves {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			served := make(chan error, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				served <- serve(w, r)
			}))
			t.Cleanup(srv.Close)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}

			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("Do() = %v, want a response", err)
			}
			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			if err == nil {
				t.Fatalf("ReadAll() = %q, nil; want an error (aborted stream)", body)
			}

			if !strings.Contains(string(body), "Ana") {
				t.Errorf("body = %q, want the rows sent before the failure", body)
			}

			if err := <-served; !errors.Is(err, export.ErrIncomplete) || !errors.Is(err, boom) {
				t.Errorf("serve() = %v, want ErrIncomplete wrapping %v", err, boom)
			}
		})
	}
}

func TestContextCancelStopsBeforeFirstRow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)

	err := export.NDJSON(rec, req, "u.ndjson", seq(user{1, "Ana"}))
	if !errors.Is(err, context.Canceled) || errors.Is(err, export.ErrIncomplete) {
		t.Fatalf("NDJSON() = %v, want context.Canceled", err)
	}

	// A client that went away is not a server error.
	if got := errs.Status(err); got != 499 {
		t.Errorf("errs.Status(%v) = %d, want 499", err, got)
	}

	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestContextCancelStopsIteration(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	pulled := 0
	rows := func(yield func(user, error) bool) {
		for i := range 1000 {
			pulled++

			if i == 3 {
				cancel()
			}

			if !yield(user{ID: i}, nil) {
				return
			}
		}
	}

	srvDone := make(chan int, 1)
	served := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { srvDone <- pulled }()

		served <- export.NDJSON(w, r.WithContext(ctx), "u.ndjson", rows, export.FlushEvery(1))
	}))
	t.Cleanup(srv.Close)

	res, err := srv.Client().Get(srv.URL)
	if err == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}

	if got := <-srvDone; got != 4 {
		t.Errorf("rows pulled = %d, want 4 (iteration stops once the context is done)", got)
	}

	err = <-served
	if !errors.Is(err, context.Canceled) || errors.Is(err, export.ErrIncomplete) || errs.Status(err) != 499 {
		t.Errorf("NDJSON() = %v (status %d), want context.Canceled with status 499", err, errs.Status(err))
	}
}

// TestErrorMidStreamWithoutHijackPanics checks the abort where the
// connection cannot be hijacked (HTTP/2, a recorder): net/http's
// ErrAbortHandler panic resets the stream.
func TestErrorMidStreamWithoutHijackPanics(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	defer func() {
		if p := recover(); p != http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
			t.Errorf("recover() = %v, want http.ErrAbortHandler", p)
		}
	}()

	err := export.NDJSON(rec, req, "u.ndjson", failing(errors.New("boom"), user{1, "Ana"}), export.FlushEvery(1))
	t.Errorf("NDJSON() = %v, want a panic", err)
}

// deadlineWriter is a ResponseWriter whose writes fail after the write
// deadline, like a connection.
type deadlineWriter struct {
	header   http.Header
	body     bytes.Buffer
	deadline time.Time
}

func (d *deadlineWriter) Header() http.Header { return d.header }

func (d *deadlineWriter) WriteHeader(int) {}

func (d *deadlineWriter) Write(b []byte) (int, error) {
	if !d.deadline.IsZero() && time.Now().After(d.deadline) {
		return 0, os.ErrDeadlineExceeded
	}

	return d.body.Write(b)
}

func (d *deadlineWriter) Flush() {}

func (d *deadlineWriter) SetWriteDeadline(t time.Time) error {
	d.deadline = t

	return nil
}

func TestWriteDeadlineFollowsWrites(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// Rows larger than the write buffer reach the client between
		// flushes; gaps stay under the timeout, the total does not.
		big := strings.Repeat("x", 5000)
		rows := func(yield func(user, error) bool) {
			for i := range 10 {
				time.Sleep(100 * time.Millisecond)

				if !yield(user{ID: i, Name: big}, nil) {
					return
				}
			}
		}

		w := &deadlineWriter{header: http.Header{}}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

		if err := export.NDJSON(w, req, "u.ndjson", rows, export.WriteTimeout(300*time.Millisecond)); err != nil {
			t.Fatalf("NDJSON() = %v, want nil", err)
		}

		if got := strings.Count(w.body.String(), "\n"); got != 10 {
			t.Errorf("rows written = %d, want 10", got)
		}

		if !w.deadline.IsZero() {
			t.Errorf("deadline after the export = %v, want cleared", w.deadline)
		}
	})
}

// flushRecorder counts flushes.
type flushRecorder struct {
	*httptest.ResponseRecorder

	flushes int
	sizes   []int
}

func (f *flushRecorder) Flush() {
	f.flushes++
	f.sizes = append(f.sizes, f.Body.Len())
}

func TestFlushCadence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rows  int
		opts  []export.Option
		flush int
	}{
		{"every 2 rows plus the end", 5, []export.Option{export.FlushEvery(2)}, 3},
		{"exact multiple", 4, []export.Option{export.FlushEvery(2)}, 2},
		{"default cadence", 10, nil, 1},
		{"below one flushes every row", 3, []export.Option{export.FlushEvery(0)}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			us := make([]user, tt.rows)
			for i := range us {
				us[i] = user{ID: i}
			}

			rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

			if err := export.NDJSON(rec, req, "u.ndjson", seq(us...), tt.opts...); err != nil {
				t.Fatal(err)
			}

			if rec.flushes != tt.flush {
				t.Errorf("flushes = %d, want %d", rec.flushes, tt.flush)
			}

			if last := rec.sizes[len(rec.sizes)-1]; last != rec.Body.Len() {
				t.Errorf("last flush saw %d bytes, want the whole body (%d)", last, rec.Body.Len())
			}
		})
	}
}

func TestContentDisposition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"users.csv", `attachment; filename="users.csv"`},
		{
			"relatório de março.csv",
			`attachment; filename="relat_rio de mar_o.csv"; filename*=UTF-8''relat%C3%B3rio%20de%20mar%C3%A7o.csv`,
		},
		{"", `attachment; filename="download"`},
		{"...", `attachment; filename="download"`},
		{"100%.csv", `attachment; filename="100_.csv"; filename*=UTF-8''100%25.csv`},
	}

	for _, tt := range tests {
		if got := export.ContentDisposition(tt.in); got != tt.want {
			t.Errorf("ContentDisposition(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSafeCell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"Ana", "Ana"},
		{"42", "42"},
		{"-42", "-42"},
		{"+3.14", "+3.14"},
		{"-0.5", "-0.5"},
		{"a=b", "a=b"},
		{" 42", " 42"},
		{"'7", "'7"},
		{"note - x", "note - x"},
	}

	for _, tt := range tests {
		if got := export.SafeCell(tt.in); got != tt.want {
			t.Errorf("SafeCell(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
