package qrcode_test

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/contrib/qrcode"
)

func TestPNGAndSVG(t *testing.T) {
	t.Parallel()

	quietZero := 0
	quietFour := 4

	pngA, err := qrcode.PNG("hello", qrcode.Options{})
	if err != nil {
		t.Fatal(err)
	}

	pngB, err := qrcode.PNG("hello", qrcode.Options{Level: qrcode.Low, Scale: qrcode.DefaultScale})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(pngA, pngB) {
		t.Fatal("default options and explicit low/scale 8 differ")
	}

	if !bytes.HasPrefix(pngA, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("png signature = %x", pngA[:8])
	}

	decoded, err := png.Decode(bytes.NewReader(pngA))
	if err != nil {
		t.Fatal(err)
	}

	if !hasBlack(decoded) {
		t.Fatal("png has no black pixel")
	}

	withQuiet, err := qrcode.PNG("hello", qrcode.Options{Scale: 4, Quiet: &quietFour})
	if err != nil {
		t.Fatal(err)
	}

	noQuiet, err := qrcode.PNG("hello", qrcode.Options{Scale: 4, Quiet: &quietZero})
	if err != nil {
		t.Fatal(err)
	}

	wide, err := png.Decode(bytes.NewReader(withQuiet))
	if err != nil {
		t.Fatal(err)
	}

	narrow, err := png.Decode(bytes.NewReader(noQuiet))
	if err != nil {
		t.Fatal(err)
	}

	if got := wide.Bounds().Dx() - narrow.Bounds().Dx(); got != 8*4 {
		t.Fatalf("quiet zone grew the image by %d px, want %d", got, 32)
	}

	svgA, err := qrcode.SVG("hello", qrcode.Options{Scale: 1, Quiet: &quietZero})
	if err != nil {
		t.Fatal(err)
	}

	svgB, err := qrcode.SVG("hello", qrcode.Options{Scale: 1, Quiet: &quietZero})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(svgA, svgB) {
		t.Fatal("svg is not deterministic")
	}

	pngMod, err := qrcode.PNG("hello", qrcode.Options{Scale: 1, Quiet: &quietZero})
	if err != nil {
		t.Fatal(err)
	}

	mod, err := png.Decode(bytes.NewReader(pngMod))
	if err != nil {
		t.Fatal(err)
	}

	x, y, ok := firstBlack(mod)
	if !ok {
		t.Fatal("no black module")
	}

	needle := "M" + strconv.Itoa(x) + " " + strconv.Itoa(y) + "h1v1h-1z"
	if !strings.Contains(string(svgA), needle) {
		t.Fatalf("svg missing black module %q", needle)
	}

	if !strings.Contains(string(svgA), "<rect ") || !strings.Contains(string(svgA), "<path ") {
		t.Fatal("svg missing rect or path")
	}
}

func TestLevelsAndRejectedOptions(t *testing.T) {
	t.Parallel()

	for _, level := range []qrcode.Level{qrcode.Low, qrcode.Medium, qrcode.Quartile, qrcode.High} {
		if _, err := qrcode.PNG("hi", qrcode.Options{Level: level, Scale: 1}); err != nil {
			t.Errorf("level %d: %v", level, err)
		}
	}

	if _, err := qrcode.PNG("", qrcode.Options{}); !errors.Is(err, qrcode.ErrEmpty) {
		t.Errorf("empty = %v, want ErrEmpty", err)
	}

	quiet := 17
	tests := []qrcode.Options{
		{Scale: 33},
		{Scale: -1},
		{Quiet: &quiet},
		{Level: 9},
	}

	for _, opt := range tests {
		if _, err := qrcode.SVG("hi", opt); !errors.Is(err, qrcode.ErrInvalidOptions) {
			t.Errorf("options %+v: %v, want ErrInvalidOptions", opt, err)
		}
	}
}

func TestHandler(t *testing.T) {
	t.Parallel()

	h := qrcode.Handler(qrcode.Options{Scale: 2})

	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/qr?data=hello", http.NoBody))

	if ok.Code != http.StatusOK || ok.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("GET = %d %q", ok.Code, ok.Header().Get("Content-Type"))
	}

	if !bytes.HasPrefix(ok.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("GET body is not a png")
	}

	again := httptest.NewRecorder()
	h.ServeHTTP(again, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/qr?data=hello", http.NoBody))

	if !bytes.Equal(ok.Body.Bytes(), again.Body.Bytes()) {
		t.Fatal("handler responses differ")
	}

	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/qr?data=hello", http.NoBody))

	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("HEAD = %d body %d type %q", head.Code, head.Body.Len(), head.Header().Get("Content-Type"))
	}

	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/qr", http.NoBody))

	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing data = %d, want 400", missing.Code)
	}

	empty := httptest.NewRecorder()
	h.ServeHTTP(empty, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/qr?data=", http.NoBody))

	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty data = %d, want 400", empty.Code)
	}

	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/qr?data=hello", http.NoBody))

	if post.Code != http.StatusMethodNotAllowed || !strings.Contains(post.Header().Get("Allow"), "GET") {
		t.Fatalf("POST = %d Allow %q", post.Code, post.Header().Get("Allow"))
	}
}

func TestSecurity_A06_QRRejectsOversize(t *testing.T) {
	t.Parallel()

	over := strings.Repeat("A", qrcode.MaxContent+1)
	if _, err := qrcode.PNG(over, qrcode.Options{}); !errors.Is(err, qrcode.ErrTooLarge) {
		t.Fatalf("oversize png = %v, want ErrTooLarge", err)
	}

	if _, err := qrcode.SVG(over, qrcode.Options{Level: qrcode.High}); !errors.Is(err, qrcode.ErrTooLarge) {
		t.Fatalf("oversize svg = %v, want ErrTooLarge", err)
	}

	// 2048 bytes is still accepted at low correction (alphanumeric fits).
	edge := strings.Repeat("A", qrcode.MaxContent)
	if _, err := qrcode.PNG(edge, qrcode.Options{Level: qrcode.Low, Scale: 1}); err != nil {
		t.Fatalf("2048 bytes at low = %v", err)
	}

	// The cap is bytes, not runes.
	runes := strings.Repeat("é", 1025) // 2050 bytes
	if _, err := qrcode.PNG(runes, qrcode.Options{}); !errors.Is(err, qrcode.ErrTooLarge) {
		t.Fatalf("rune oversize = %v, want ErrTooLarge", err)
	}

	h := qrcode.Handler(qrcode.Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/qr", http.NoBody)
	req.URL.RawQuery = "data=" + over
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handler oversize = %d, want 400", rec.Code)
	}

	if bytes.Contains(rec.Body.Bytes(), []byte(over[:32])) {
		t.Fatal("handler echoed the payload")
	}
}

func hasBlack(img image.Image) bool {
	_, _, ok := firstBlack(img)

	return ok
}

func firstBlack(img image.Image) (int, int, bool) {
	b := img.Bounds()

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r == 0 && g == 0 && bl == 0 {
				return x, y, true
			}
		}
	}

	return 0, 0, false
}
