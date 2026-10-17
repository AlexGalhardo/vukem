package totp

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// rawModules counts the data modules of ver independently of newQR: the
// symbol minus finder, separator, format, timing, alignment and version
// patterns (ISO/IEC 18004 table 1).
func rawModules(ver int) int {
	size := 17 + 4*ver
	n := size*size - 3*64 - 31 - 2*(size-16)

	if ver >= 2 {
		a := ver/7 + 2
		n -= (a*a - 3) * 25
		n += 2 * (a - 2) * 5 // alignment patterns crossing the timing lines
	}

	if ver >= 7 {
		n -= 36
	}

	return n
}

func TestQRVersionTables(t *testing.T) {
	t.Parallel()

	for ver := 1; ver < len(qrVersions); ver++ {
		v := qrVersions[ver]

		blocks := 0
		for _, b := range v.blocks {
			blocks += b[0]
		}

		if got, want := v.dataCodewords()+blocks*v.ec, rawModules(ver)/8; got != want {
			t.Errorf("version %d: %d codewords, want %d", ver, got, want)
		}

		reserved := 0
		for _, row := range newQR(ver).reserved {
			for _, r := range row {
				if r {
					reserved++
				}
			}
		}

		size := 17 + 4*ver
		if got, want := size*size-reserved, rawModules(ver); got != want {
			t.Errorf("newQR(%d): %d data modules, want %d", ver, got, want)
		}
	}
}

func TestQRAlign(t *testing.T) {
	t.Parallel()

	// ISO/IEC 18004 annex E, table E.1.
	want := map[int][]int{
		1:  nil,
		2:  {6, 18},
		7:  {6, 22, 38},
		10: {6, 28, 50},
		14: {6, 26, 46, 66},
		16: {6, 26, 50, 74},
		22: {6, 26, 50, 74, 98},
		30: {6, 26, 52, 78, 104, 130},
		32: {6, 34, 60, 86, 112, 138},
		36: {6, 24, 50, 76, 102, 128, 154},
		39: {6, 26, 54, 82, 110, 138, 166},
		40: {6, 30, 58, 86, 114, 142, 170},
	}

	for ver, w := range want {
		if diff := cmp.Diff(w, qrAlign(ver)); diff != "" {
			t.Errorf("qrAlign(%d) (-want +got):\n%s", ver, diff)
		}
	}
}
