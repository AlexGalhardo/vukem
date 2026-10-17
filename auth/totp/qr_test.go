package totp_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth/totp"
)

// testdata/qr.golden holds matrices (versions 1 to 40) verified by decoding
// them with an independent QR reader (jsQR); this test keeps the encoder
// from drifting.
func TestQRMatrixGolden(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("testdata/qr.golden")
	if err != nil {
		t.Fatal(err)
	}

	for blk := range strings.SplitSeq(strings.ReplaceAll(string(b), "\r\n", "\n"), "--\n") {
		lines := strings.Split(strings.TrimSpace(blk), "\n")
		if len(lines) < 2 {
			continue
		}

		m, err := totp.QRMatrix(lines[0])
		if err != nil {
			t.Fatalf("QRMatrix(%q) = %v", lines[0], err)
		}

		var got []string

		for _, row := range m {
			var s strings.Builder
			for _, d := range row {
				if d {
					s.WriteByte('1')
				} else {
					s.WriteByte('0')
				}
			}

			got = append(got, s.String())
		}

		if strings.Join(got, "\n") != strings.Join(lines[1:], "\n") {
			t.Errorf("QR matrix of %.20q changed", lines[0])
		}
	}
}

func TestQRSVG(t *testing.T) {
	t.Parallel()

	svg, err := totp.QR(totp.URI("Shop", "ana@example.com", "JBSWY3DPEHPK3PXP", totp.Options{}), 200)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 49 49" width="200"`) ||
		!strings.Contains(svg, `aria-label="QR code"`) || strings.Contains(svg, "ana@") {
		t.Errorf("SVG = %.200s", svg)
	}

	// 2331 bytes is the byte-mode capacity of version 40 at level M.
	if svg, err := totp.QR(strings.Repeat("x", 2331), 200); err != nil ||
		!strings.Contains(svg, `viewBox="0 0 185 185"`) {
		t.Errorf("QR(2331 bytes) = %.80s, %v; want a version 40 code", svg, err)
	}

	if _, err := totp.QR(strings.Repeat("x", 2332), 200); !errors.Is(err, totp.ErrTooLong) {
		t.Errorf("QR(2332 bytes) error = %v, want ErrTooLong", err)
	}
}
