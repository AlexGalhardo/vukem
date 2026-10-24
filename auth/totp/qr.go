package totp

import (
	"errors"
	"fmt"
	"strings"
)

// QR encoding (ISO/IEC 18004) of the enrolment URI, so the starter can show
// a scannable code without a dependency: byte mode, error correction level
// M, versions 1–40 (up to 2331 bytes: otpauth URIs, PIX BR Codes).

// ErrTooLong is returned when the text does not fit a version 40 code.
var ErrTooLong = errors.New("totp: text too long for a QR code")

type qrVersion struct {
	ec     int      // EC codewords per block
	blocks [][2]int // {count, data codewords per block}
}

// qrVersions is the level M block structure (ISO/IEC 18004 table 9).
var qrVersions = [41]qrVersion{
	{},
	{10, [][2]int{{1, 16}}},
	{16, [][2]int{{1, 28}}},
	{26, [][2]int{{1, 44}}},
	{18, [][2]int{{2, 32}}},
	{24, [][2]int{{2, 43}}},
	{16, [][2]int{{4, 27}}},
	{18, [][2]int{{4, 31}}},
	{22, [][2]int{{2, 38}, {2, 39}}},
	{22, [][2]int{{3, 36}, {2, 37}}},
	{26, [][2]int{{4, 43}, {1, 44}}},
	{30, [][2]int{{1, 50}, {4, 51}}},
	{22, [][2]int{{6, 36}, {2, 37}}},
	{22, [][2]int{{8, 37}, {1, 38}}},
	{24, [][2]int{{4, 40}, {5, 41}}},
	{24, [][2]int{{5, 41}, {5, 42}}},
	{28, [][2]int{{7, 45}, {3, 46}}},
	{28, [][2]int{{10, 46}, {1, 47}}},
	{26, [][2]int{{9, 43}, {4, 44}}},
	{26, [][2]int{{3, 44}, {11, 45}}},
	{26, [][2]int{{3, 41}, {13, 42}}},
	{26, [][2]int{{17, 42}}},
	{28, [][2]int{{17, 46}}},
	{28, [][2]int{{4, 47}, {14, 48}}},
	{28, [][2]int{{6, 45}, {14, 46}}},
	{28, [][2]int{{8, 47}, {13, 48}}},
	{28, [][2]int{{19, 46}, {4, 47}}},
	{28, [][2]int{{22, 45}, {3, 46}}},
	{28, [][2]int{{3, 45}, {23, 46}}},
	{28, [][2]int{{21, 45}, {7, 46}}},
	{28, [][2]int{{19, 47}, {10, 48}}},
	{28, [][2]int{{2, 46}, {29, 47}}},
	{28, [][2]int{{10, 46}, {23, 47}}},
	{28, [][2]int{{14, 46}, {21, 47}}},
	{28, [][2]int{{14, 46}, {23, 47}}},
	{28, [][2]int{{12, 47}, {26, 48}}},
	{28, [][2]int{{6, 47}, {34, 48}}},
	{28, [][2]int{{29, 46}, {14, 47}}},
	{28, [][2]int{{13, 46}, {32, 47}}},
	{28, [][2]int{{40, 47}, {7, 48}}},
	{28, [][2]int{{18, 47}, {31, 48}}},
}

// qrAlign returns the alignment pattern centres of ver (ISO/IEC 18004
// annex E): 6, then evenly spaced (even steps) up to size-7.
func qrAlign(ver int) []int {
	if ver == 1 {
		return nil
	}

	n := ver/7 + 2
	size := 17 + 4*ver

	step := 26 // version 32 is the one exception to the formula
	if ver != 32 {
		step = (ver*4 + n*2 + 1) / (n*2 - 2) * 2
	}

	pos := make([]int, n)
	pos[0] = 6

	for i := n - 1; i > 0; i-- {
		pos[i] = size - 7 - (n-1-i)*step
	}

	return pos
}

func (v qrVersion) dataCodewords() int {
	n := 0
	for _, b := range v.blocks {
		n += b[0] * b[1]
	}

	return n
}

// QRMatrix encodes text and returns the module matrix (true = dark),
// without the quiet zone.
func QRMatrix(text string) ([][]bool, error) {
	data := []byte(text)

	ver := 0

	for v := 1; v < len(qrVersions); v++ {
		countBits := 8
		if v >= 10 {
			countBits = 16
		}

		if 4+countBits+8*len(data) <= 8*qrVersions[v].dataCodewords() {
			ver = v
			break
		}
	}

	if ver == 0 {
		return nil, ErrTooLong
	}

	codewords := qrCodewords(data, ver)
	q := newQR(ver)
	q.place(codewords)

	best, bestPenalty := -1, 0

	for mask := range 8 {
		c := q.clone()
		c.mask(mask)
		c.format(mask)

		if p := c.penalty(); best < 0 || p < bestPenalty {
			best, bestPenalty = mask, p
		}
	}

	q.mask(best)
	q.format(best)

	return q.dark, nil
}

// QR returns an SVG image of the QR code of text (e.g. a totp.URI), with
// a 4-module quiet zone, scaled to size pixels.
func QR(text string, size int) (string, error) {
	m, err := QRMatrix(text)
	if err != nil {
		return "", err
	}

	n := len(m) + 8

	var b strings.Builder

	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" `+
		`shape-rendering="crispEdges" role="img" aria-label="QR code"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`,
		n, n, size, size, n, n)

	for y, row := range m {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}

	b.WriteString(`"/></svg>`)

	return b.String(), nil
}

// qrCodewords builds the data bit stream and interleaves data and
// Reed-Solomon codewords.
func qrCodewords(data []byte, ver int) []byte {
	v := qrVersions[ver]

	var bits []bool

	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, val>>i&1 == 1)
		}
	}

	countBits := 8
	if ver >= 10 {
		countBits = 16
	}

	put(0b0100, 4)
	put(len(data), countBits)

	for _, c := range data {
		put(int(c), 8)
	}

	capBits := 8 * v.dataCodewords()
	put(0, min(4, capBits-len(bits)))

	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}

	stream := make([]byte, 0, v.dataCodewords())
	for i := 0; i < len(bits); i += 8 {
		var c byte
		for j := range 8 {
			if bits[i+j] {
				c |= 1 << (7 - j)
			}
		}

		stream = append(stream, c)
	}

	for pad := 0; len(stream) < v.dataCodewords(); pad++ {
		stream = append(stream, [2]byte{0xEC, 0x11}[pad%2])
	}

	var dataBlocks, ecBlocks [][]byte

	off := 0

	for _, g := range v.blocks {
		for range g[0] {
			blk := stream[off : off+g[1]]
			off += g[1]
			dataBlocks = append(dataBlocks, blk)
			ecBlocks = append(ecBlocks, rsEncode(blk, v.ec))
		}
	}

	var out []byte

	for i := range dataBlocks[len(dataBlocks)-1] {
		for _, blk := range dataBlocks {
			if i < len(blk) {
				out = append(out, blk[i])
			}
		}
	}

	for i := range v.ec {
		for _, blk := range ecBlocks {
			out = append(out, blk[i])
		}
	}

	return out
}

// GF(256) with the QR polynomial x^8+x^4+x^3+x^2+1.
var gfExp, gfLog = func() (exp [512]byte, log [256]byte) {
	x := 1
	for i := range 255 {
		exp[i] = byte(x)
		log[x] = byte(i)

		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}

	for i := 255; i < 512; i++ {
		exp[i] = exp[i-255]
	}

	return exp, log
}()

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}

	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func rsEncode(data []byte, ecLen int) []byte {
	gen := []byte{1}

	for i := range ecLen {
		next := make([]byte, len(gen)+1)

		for j, g := range gen {
			next[j] ^= g
			next[j+1] ^= gfMul(g, gfExp[i])
		}

		gen = next
	}

	rem := make([]byte, ecLen)

	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[ecLen-1] = 0

		for i := range ecLen {
			rem[i] ^= gfMul(gen[i+1], factor)
		}
	}

	return rem
}

type qr struct {
	ver      int
	size     int
	dark     [][]bool
	reserved [][]bool
}

func newQR(ver int) *qr {
	size := 17 + 4*ver
	q := &qr{ver: ver, size: size, dark: grid(size), reserved: grid(size)}

	for _, p := range [][2]int{{0, 0}, {size - 7, 0}, {0, size - 7}} {
		q.finder(p[0], p[1])
	}

	for i := 8; i < size-8; i++ {
		q.set(i, 6, i%2 == 0)
		q.set(6, i, i%2 == 0)
	}

	align := qrAlign(ver)
	for _, cy := range align {
		for _, cx := range align {
			if (cx == 6 && cy == 6) || (cx == 6 && cy == size-7) || (cx == size-7 && cy == 6) {
				continue
			}

			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					q.set(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}

	// Format areas (filled in by format) and the dark module.
	for i := range 9 {
		q.reserved[8][i], q.reserved[i][8] = true, true
	}

	for i := range 8 {
		q.reserved[8][size-1-i], q.reserved[size-1-i][8] = true, true
	}

	q.set(8, size-8, true)

	if ver >= 7 {
		q.version()
	}

	return q
}

func grid(n int) [][]bool {
	g := make([][]bool, n)
	for i := range g {
		g[i] = make([]bool, n)
	}

	return g
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}

// set marks a function module (x column, y row).
func (q *qr) set(x, y int, dark bool) {
	q.dark[y][x] = dark
	q.reserved[y][x] = true
}

func (q *qr) finder(x0, y0 int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			x, y := x0+dx, y0+dy
			if x < 0 || y < 0 || x >= q.size || y >= q.size {
				continue
			}

			d := max(abs(dx-3), abs(dy-3))
			q.set(x, y, d != 2 && d != 4)
		}
	}
}

func (q *qr) version() {
	bits := q.ver << 12

	rem := bits
	for i := 17; i >= 12; i-- {
		if rem>>i&1 == 1 {
			rem ^= 0x1F25 << (i - 12)
		}
	}

	bits |= rem

	for i := range 18 {
		dark := bits>>i&1 == 1
		a, b := i/3, q.size-11+i%3
		q.set(b, a, dark)
		q.set(a, b, dark)
	}
}

// place writes codewords in the zigzag order, skipping function modules.
func (q *qr) place(codewords []byte) {
	bit := 0
	total := len(codewords) * 8

	for right := q.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}

		for vert := range q.size {
			for j := range 2 {
				x := right - j
				upward := (right+1)&2 == 0

				y := vert
				if upward {
					y = q.size - 1 - vert
				}

				if q.reserved[y][x] {
					continue
				}

				if bit < total {
					q.dark[y][x] = codewords[bit/8]>>(7-bit%8)&1 == 1
				}

				bit++
			}
		}
	}
}

func (q *qr) clone() *qr {
	c := &qr{ver: q.ver, size: q.size, dark: grid(q.size), reserved: q.reserved}
	for i := range q.dark {
		copy(c.dark[i], q.dark[i])
	}

	return c
}

func (q *qr) mask(m int) {
	for y := range q.size {
		for x := range q.size {
			if q.reserved[y][x] {
				continue
			}

			var flip bool

			switch m {
			case 0:
				flip = (x+y)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (x+y)%3 == 0
			case 4:
				flip = (x/3+y/2)%2 == 0
			case 5:
				flip = x*y%2+x*y%3 == 0
			case 6:
				flip = (x*y%2+x*y%3)%2 == 0
			case 7:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}

			if flip {
				q.dark[y][x] = !q.dark[y][x]
			}
		}
	}
}

// format writes the 15-bit format information (level M = 00).
func (q *qr) format(mask int) {
	data := mask // EC level M is 00
	rem := data << 10

	for i := 14; i >= 10; i-- {
		if rem>>i&1 == 1 {
			rem ^= 0x537 << (i - 10)
		}
	}

	bits := (data<<10 | rem) ^ 0x5412

	bit := func(i int) bool { return bits>>i&1 == 1 }

	for i := range 6 {
		q.dark[i][8] = bit(i)
	}

	q.dark[7][8] = bit(6)
	q.dark[8][8] = bit(7)
	q.dark[8][7] = bit(8)

	for i := 9; i < 15; i++ {
		q.dark[8][14-i] = bit(i)
	}

	for i := range 8 {
		q.dark[8][q.size-1-i] = bit(i)
	}

	for i := 8; i < 15; i++ {
		q.dark[q.size-15+i][8] = bit(i)
	}

	q.dark[q.size-8][8] = true
}

//nolint:gocognit // QR mask penalty rules straight from ISO/IEC 18004
func (q *qr) penalty() int {
	p := 0
	n := q.size

	at := func(x, y int, col bool) bool {
		if col {
			return q.dark[x][y]
		}

		return q.dark[y][x]
	}

	pattern := []bool{true, false, true, true, true, false, true}

	for _, col := range []bool{false, true} {
		for y := range n {
			run := 1

			for x := 1; x < n; x++ {
				if at(x, y, col) == at(x-1, y, col) {
					run++
					continue
				}

				if run >= 5 {
					p += run - 2
				}

				run = 1
			}

			if run >= 5 {
				p += run - 2
			}

			for x := 0; x+7 <= n; x++ {
				match := true
				for i, want := range pattern {
					if at(x+i, y, col) != want {
						match = false
						break
					}
				}

				if !match {
					continue
				}

				light := func(from, to int) bool {
					for i := from; i < to; i++ {
						if i >= 0 && i < n && at(i, y, col) {
							return false
						}
					}

					return true
				}

				if light(x-4, x) || light(x+7, x+11) {
					p += 40
				}
			}
		}
	}

	darkCount := 0

	for y := range n {
		for x := range n {
			if q.dark[y][x] {
				darkCount++
			}

			if x+1 < n && y+1 < n {
				c := q.dark[y][x]
				if q.dark[y][x+1] == c && q.dark[y+1][x] == c && q.dark[y+1][x+1] == c {
					p += 3
				}
			}
		}
	}

	pct := darkCount * 100 / (n * n)
	p += abs(pct-50) / 5 * 10

	return p
}
