package web

import (
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/errs"
)

// TestSecurity_A05_DownloadFilename checks that c.Download sanitizes the
// download name like export.ContentDisposition and sets the header only
// when the file is served.
func TestSecurity_A05_DownloadFilename(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"files/a.txt": {Data: []byte("hello")}}

	tests := []struct {
		filename, want string
	}{
		{"report.txt", "report.txt"},
		{"../../etc/passwd", "passwd"},
		{`..\..\win.ini`, "win.ini"},
		{"x.txt\r\nSet-Cookie: a=1", "x.txtSet-Cookie_ a=1"},
		{"invoice\u202etxt.exe", "invoicetxt.exe"},
		{"relatório.txt", "relatório.txt"},
		{"", "a.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			t.Parallel()

			c, rec := newCtx(t, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), nil)
			if err := c.Download(fsys, "files/a.txt", tt.filename); err != nil {
				t.Fatal(err)
			}

			v := rec.Header().Get("Content-Disposition")

			disp, params, err := mime.ParseMediaType(v)
			if err != nil || disp != "attachment" || params["filename"] != tt.want {
				t.Errorf(
					"Content-Disposition = %q (%q, %v), want attachment for %q",
					v,
					params["filename"],
					err,
					tt.want,
				)
			}
		})
	}

	t.Run("missing file sets no header", func(t *testing.T) {
		t.Parallel()

		c, rec := newCtx(t, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), nil)
		if err := c.Download(fsys, "files/missing.txt", "x.txt"); !errors.Is(err, errs.ErrNotFound) {
			t.Fatalf("Download(missing) = %v, want ErrNotFound", err)
		}

		if v := rec.Header().Get("Content-Disposition"); v != "" {
			t.Errorf("Content-Disposition = %q, want none on an error", v)
		}
	})
}
