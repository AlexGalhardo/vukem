package export_test

import (
	"encoding/csv"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/export"
)

// TestSecurity_A05_ExportCSVFormulaInjection checks that every CSV cell
// (header and rows, CSV, WriteCSV and EncodeCSV) starting with a formula trigger is
// written as text, while plain numbers stay numbers (OWASP CSV injection).
func TestSecurity_A05_ExportCSVFormulaInjection(t *testing.T) {
	t.Parallel()

	payloads := []string{
		"=HYPERLINK(\"http://evil\",\"x\")",
		"+1+cmd|' /C calc'!A0",
		"-2+3",
		"@SUM(A1:A2)",
		"\t=1+1",
		"\r=1+1",
		"-",
		"+",
		" =1+1",
		"\n=1+1",
		"\"=1+1\"",
		"'@SUM(A1)",
		"  -2+3",
	}

	want := make([]string, len(payloads))
	for i, p := range payloads {
		want[i] = "'" + p
	}

	serves := map[string]func(w http.ResponseWriter, r *http.Request) error{
		"csv": func(w http.ResponseWriter, r *http.Request) error {
			return export.CSV(w, r, "x.csv", payloads, seq(user{}), func(user) []string { return payloads })
		},
		"encode": func(w http.ResponseWriter, _ *http.Request) error {
			return export.EncodeCSV(w, payloads, seq(user{}), func(user) []string { return payloads })
		},
		"csv writer": func(w http.ResponseWriter, r *http.Request) error {
			return export.WriteCSV(w, r, "x.csv", func(cw *export.CSVWriter) error {
				if err := cw.Write(payloads); err != nil {
					return err
				}

				return cw.Write(payloads)
			})
		},
	}

	for name, serve := range serves {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

			if err := serve(rec, req); err != nil {
				t.Fatal(err)
			}

			got, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
			if err != nil {
				t.Fatalf("ReadAll() = %v", err)
			}

			if diff := cmp.Diff([][]string{want, want}, got); diff != "" {
				t.Errorf("cells not neutralised (-want +got):\n%s", diff)
			}
		})
	}

	for _, p := range payloads {
		if got := export.SafeCell(p); got != "'"+p {
			t.Errorf("SafeCell(%q) = %q, want %q", p, got, "'"+p)
		}
	}

	t.Run("insecure raw cells opt-out", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

		err := export.CSV(
			rec,
			req,
			"x.csv",
			[]string{"f"},
			seq(user{}),
			func(user) []string { return []string{"=1+1"} },
			export.InsecureRawCells(),
		)
		if err != nil {
			t.Fatal(err)
		}

		if got, want := rec.Body.String(), "f\n=1+1\n"; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
	})
}

// TestSecurity_A05_ExportFilenameHeaderInjection checks that a hostile
// download name cannot add headers or parameters, traverse paths, or hide
// its extension, and that the header always parses.
func TestSecurity_A05_ExportFilenameHeaderInjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string // the parsed file name
	}{
		{"a.csv\r\nSet-Cookie: x=1", "a.csvSet-Cookie_ x=1"},
		{"a\".csv; filename=evil.exe", "a_.csv; filename=evil.exe"},
		{"../../etc/passwd", "passwd"},
		{`..\..\windows\win.ini`, "win.ini"},
		{"C:\\Users\\x\\report.csv", "report.csv"},
		{"invoice\u202Efdp.exe", "invoicefdp.exe"},
		{".env", "env"},
		{"name\x00.csv", "name.csv"},
		{"a\u0085b\u2028c\u2029d.csv", "abcd.csv"},
		{"report.csv. . ", "report.csv"},
		{"CON", "_CON"},
		{"nul.csv", "_nul.csv"},
		{"Com1.txt", "_Com1.txt"},
		{"LPT9", "_LPT9"},
		{"COM0.txt", "COM0.txt"},
		{"CONSOLE.csv", "CONSOLE.csv"},
		{"é" + strings.Repeat("a", 300) + ".csv", "é" + strings.Repeat("a", 195) + ".csv"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			v := export.ContentDisposition(tt.in)
			if strings.ContainsAny(v, "\r\n\x00") {
				t.Fatalf("ContentDisposition(%q) = %q, contains a control character", tt.in, v)
			}

			disp, params, err := mime.ParseMediaType(v)
			if err != nil {
				t.Fatalf("ParseMediaType(%q) = %v", v, err)
			}

			if disp != "attachment" {
				t.Errorf("disposition = %q, want attachment", disp)
			}

			// mime prefers filename* (RFC 5987) when present, as browsers do.
			if params["filename"] != tt.want {
				t.Errorf("filename = %q, want %q (header %q)", params["filename"], tt.want, v)
			}

			for k := range params {
				if k != "filename" {
					t.Errorf("unexpected parameter %q in %q", k, v)
				}
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

			if err := export.NDJSON(rec, req, tt.in, seq()); err != nil {
				t.Fatal(err)
			}

			if got := rec.Header().Get("Content-Disposition"); got != v {
				t.Errorf("Content-Disposition = %q, want %q", got, v)
			}
		})
	}
}
