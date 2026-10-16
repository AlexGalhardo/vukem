package z

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// fileHeader builds a real *multipart.FileHeader through a parsed form.
func fileHeader(t *testing.T, name string, content []byte) *multipart.FileHeader {
	t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	w, err := mw.CreateFormFile("f", name)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = w.Write(content)
	_ = mw.Close()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = r.MultipartForm.RemoveAll() })

	return r.MultipartForm.File["f"][0]
}

func TestFile(t *testing.T) {
	t.Parallel()

	png := []byte("\x89PNG\r\n\x1a\n" + "rest")
	schema := File().MaxSize(100).Types("image/png").Extensions("png")

	tests := []struct {
		name string
		in   any
		want []Issue
	}{
		{name: "valid", in: fileHeader(t, "a.PNG", png)},
		{
			name: "too big",
			in:   fileHeader(t, "a.png", append(png, bytes.Repeat([]byte("x"), 200)...)),
			want: []Issue{{
				Code: CodeTooBig, Key: "too_big.file", Message: "The value field must not be greater than 0 kilobytes.",
				Params: map[string]any{"max": int64(100), "max_kb": int64(0)},
			}},
		},
		{
			name: "spoofed type",
			in:   fileHeader(t, "a.png", []byte("<html><body>hi</body></html>")),
			want: []Issue{{
				Code: CodeInvalidFile, Key: "file.type", Message: "The value field must be a file of type: image/png.",
				Params: map[string]any{"types": "image/png"},
			}},
		},
		{
			name: "wrong extension",
			in:   fileHeader(t, "a.exe", png),
			want: []Issue{{
				Code: CodeInvalidFile, Key: "file.extension",
				Message: "The value field must have one of these extensions: .png.",
				Params:  map[string]any{"extensions": ".png"},
			}},
		},
		{
			name: "not a file",
			in:   "a.png",
			want: []Issue{{
				Code: CodeInvalidType, Key: CodeInvalidType, Message: "The value field must be a file.",
				Params: map[string]any{"expected": "a file", "received": "string"},
			}},
		},
		{
			name: "absent",
			in:   nil,
			want: []Issue{{Code: CodeRequired, Key: CodeRequired, Message: "The value field is required."}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := schema.Parse(tt.in)

			var got []Issue
			if is, ok := AsIssues(err); ok {
				got = is
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Parse() issues (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFileInStruct(t *testing.T) {
	t.Parallel()

	type Profile struct {
		Name   string                `json:"name"`
		Avatar *multipart.FileHeader `json:"avatar"`
	}

	schema := Struct[Profile](Shape{"name": String(), "avatar": File().Optional().Types("image/png")})
	fh := fileHeader(t, "me.png", []byte("\x89PNG\r\n\x1a\nxx"))

	got, err := schema.Parse(map[string]any{"name": "Al", "avatar": fh})
	if err != nil || got.Avatar != fh {
		t.Fatalf("Parse() = %+v, %v", got, err)
	}

	if got, err := schema.Parse(map[string]any{"name": "Al", "avatar": ""}); err != nil || got.Avatar != nil {
		t.Fatalf("Parse(no file chosen) = %+v, %v, want no avatar", got, err)
	}

	if err := File().Types("image/png").Check(fh); err != nil {
		t.Errorf("Check() = %v", err)
	}

	if diff := cmp.Diff(map[string]any{"type": "string", "format": "binary", "contentMediaType": "image/png"},
		JSONSchema(File().Types("image/png"))); diff != "" {
		t.Errorf("JSONSchema (-want +got):\n%s", diff)
	}
}
