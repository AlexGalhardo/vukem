package upload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/web"
)

func FuzzSanitizeName(f *testing.F) {
	for _, s := range []string{
		"photo.jpg", "../../etc/passwd", `..\..\boot.ini`, "/abs/path.png", "C:\\x\\y.gif", ".htaccess",
		"a\x00b.png", `quo"te.jpg`, "..", " . ", strings.Repeat("é", 300) + ".jpeg", "\xff\xfe.png",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		got := SanitizeName(name)

		switch {
		case got == "":
			t.Fatalf("SanitizeName(%q) is empty", name)
		case strings.ContainsAny(got, "/\\\"\x7f"), strings.ContainsFunc(got, func(r rune) bool { return r < 0x20 }):
			t.Fatalf("SanitizeName(%q) = %q, keeps a separator, quote or control character", name, got)
		case strings.ContainsFunc(got, func(r rune) bool { return unicode.Is(unicode.Cf, r) }):
			t.Fatalf("SanitizeName(%q) = %q, keeps a format (bidi or zero-width) character", name, got)
		case strings.HasPrefix(got, "."):
			t.Fatalf("SanitizeName(%q) = %q, is a dotfile", name, got)
		case !utf8.ValidString(got), utf8.RuneCountInString(got) > maxNameLen:
			t.Fatalf("SanitizeName(%q) = %q, invalid UTF-8 or longer than %d runes", name, got, maxNameLen)
		}
	})
}

func FuzzParseMetadata(f *testing.F) {
	for _, s := range []string{
		"filename ZG9nLnBuZw==", "filename ZG9nLnBuZw==,filetype aW1hZ2UvcG5n", "is_confidential",
		"a b,,", " k  dg== ", "\x00 x", "filename !!!", strings.Repeat("k dg==,", 40),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, header string) {
		m, err := parseMetadata(header)
		if err != nil {
			return
		}

		if len(m) > maxMetadataPairs {
			t.Fatalf("parseMetadata(%q) returned %d pairs, want at most %d", header, len(m), maxMetadataPairs)
		}

		for k, v := range m {
			if k == "" || strings.ContainsFunc(k, func(r rune) bool { return r <= ' ' || r > '~' }) ||
				!utf8.ValidString(v) {
				t.Fatalf("parseMetadata(%q) accepted key %q value %q", header, k, v)
			}
		}

		again, err := parseMetadata(encodeMetadata(m))
		if err != nil || !maps.Equal(again, m) && len(m) > 0 {
			t.Fatalf("metadata round trip of %q = %v, %v, want %v", header, again, err, m)
		}
	})
}

func FuzzParseClamReply(f *testing.F) {
	for _, s := range []string{"stream: OK", "stream: Eicar-Signature FOUND", "stream: ERROR", "", "OK", "FOUND"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, reply string) {
		err := parseClamReply(reply)
		// Anything that is not an explicit clean answer must reject the file.
		if err == nil && !strings.HasSuffix(strings.TrimRight(reply, "\x00\r\n "), "OK") {
			t.Fatalf("parseClamReply(%q) = nil, want an error for a non-OK reply", reply)
		}
	})
}

// FuzzReceive feeds raw multipart bodies to Receive: failures are client
// errors that leave no file behind; stored files have random names whose
// extension matches the detected type.
func FuzzReceive(f *testing.F) {
	const boundary = "fuzzboundary"

	seed := func(parts ...string) []byte {
		var b bytes.Buffer
		for _, p := range parts {
			b.WriteString("--" + boundary + "\r\n" + p + "\r\n")
		}
		b.WriteString("--" + boundary + "--\r\n")

		return b.Bytes()
	}

	f.Add(seed("Content-Disposition: form-data; name=\"title\"\r\n\r\nhello"))
	f.Add(seed("Content-Disposition: form-data; name=\"file\"; filename=\"a.png\"\r\nContent-Type: image/png\r\n\r\n" +
		pngMagic + "data"))
	f.Add(seed("Content-Disposition: form-data; name=\"file\"; filename=\"../../x.png\"\r\n\r\n" + pngMagic))
	f.Add(seed("Content-Disposition: form-data; name=\"file\"; filename=\"x.png\"\r\n\r\n<html>"))
	f.Add([]byte("--" + boundary + "\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\nunterminated"))

	rules := Images(1 << 12)
	rules.MaxFiles = 2
	rules.MaxFields = 4
	rules.MaxFieldSize = 256
	rules.Dir = "uploads"

	f.Fuzz(func(t *testing.T, body []byte) {
		d := storage.NewMemory("")
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", bytes.NewReader(body))
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

		form, err := Receive(web.NewCtx(httptest.NewRecorder(), r, nil), d, rules)

		stored, lerr := d.List(context.Background(), "")
		if lerr != nil {
			t.Fatal(lerr)
		}

		if err != nil {
			if st := errs.Status(err); st < 400 || st >= 500 {
				t.Fatalf("Receive() error = %v (status %d), want a 4xx", err, st)
			}

			if len(stored) > 0 {
				t.Fatalf("rejected upload left files %v", stored)
			}

			return
		}

		for _, file := range form.Files {
			dir, base := path.Split(file.Path)
			if dir != "uploads/" || strings.HasPrefix(base, ".") || strings.ContainsAny(base, `\:`) ||
				!slices.Contains(Extensions(file.Type), path.Ext(base)) {
				t.Fatalf("stored path %q (type %s) is not a random name under uploads/", file.Path, file.Type)
			}
		}
	})
}

// FuzzTusRequest sends creation and PATCH requests with fuzzed tus headers:
// the handler answers without a server error and never stores more than
// Upload-Length bytes.
func FuzzTusRequest(f *testing.F) {
	f.Add("10", "filename ZG9nLnBuZw==", "0", "application/offset+octet-stream", []byte("0123456789"))
	f.Add("-1", "", "0", "application/offset+octet-stream", []byte("x"))
	f.Add("99999999999999999999", "a", "x", "text/plain", []byte(""))
	f.Add("4", "k dg==", "2", "application/offset+octet-stream", []byte("abcdef"))

	cfg := TusConfig{
		Dir:       f.TempDir(),
		BasePath:  "/files/",
		MaxSize:   64,
		Authorize: func(*http.Request) error { return nil },
		OnComplete: func(_ context.Context, info TusInfo, open func() (io.ReadCloser, error)) error {
			rc, err := open()
			if err != nil {
				return err
			}
			defer rc.Close()

			b, err := io.ReadAll(rc)
			if err != nil {
				return err
			}

			if int64(len(b)) != info.Length {
				return errors.New("completed upload length mismatch")
			}

			return nil
		},
	}

	h, err := Tus(cfg)
	if err != nil {
		f.Fatal(err)
	}

	f.Cleanup(func() { _ = h.Close() })

	f.Fuzz(func(t *testing.T, length, metadata, offset, contentType string, body []byte) {
		do := func(method, target string, hdr map[string]string, body io.Reader) *httptest.ResponseRecorder {
			r := httptest.NewRequestWithContext(t.Context(), method, target, body)
			r.Header.Set("Tus-Resumable", TusVersion)

			for k, v := range hdr {
				r.Header.Set(k, v)
			}

			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code >= 500 {
				t.Fatalf("%s %s %v = %d, want no server error", method, target, hdr, w.Code)
			}

			return w
		}

		created := do(
			http.MethodPost,
			"/files/",
			map[string]string{"Upload-Length": length, "Upload-Metadata": metadata},
			http.NoBody,
		)

		loc := created.Header().Get("Location")
		if created.Code != http.StatusCreated {
			return
		}

		if !strings.HasPrefix(loc, "/files/") || !validID(strings.TrimPrefix(loc, "/files/")) {
			t.Fatalf("Location = %q, want /files/<random id>", loc)
		}

		do(http.MethodPatch, loc, map[string]string{"Upload-Offset": offset, "Content-Type": contentType},
			bytes.NewReader(body))
		do(http.MethodDelete, loc, nil, http.NoBody)
	})
}
