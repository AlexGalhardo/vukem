package webauthn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// errCBOR reports input the decoder does not accept.
var errCBOR = errors.New("webauthn: malformed CBOR")

// maxCBORDepth bounds nesting: WebAuthn structures nest at most three
// levels (attestation object, attStmt, x5c), so deeper input is hostile.
const maxCBORDepth = 8

// cborDecoder reads the CBOR subset WebAuthn authenticators emit (CTAP2
// canonical: definite lengths, no tags, no floats). Every length is checked
// against the bytes left before anything is allocated.
type cborDecoder struct {
	data []byte
	pos  int
}

// decodeCBOR decodes one item from data and returns it with the number of
// bytes it used. Maps decode to map[any]any with int64, string or bool
// keys; byte strings to []byte; text to string; unsigned and negative
// integers to int64 (values beyond int64 are rejected).
func decodeCBOR(data []byte) (any, int, error) {
	d := &cborDecoder{data: data}

	v, err := d.item(0)
	if err != nil {
		return nil, 0, err
	}

	return v, d.pos, nil
}

func (d *cborDecoder) item(depth int) (any, error) {
	if depth > maxCBORDepth {
		return nil, fmt.Errorf("%w: nested too deep", errCBOR)
	}

	major, arg, err := d.head()
	if err != nil {
		return nil, err
	}

	switch major {
	case 0:
		if arg > math.MaxInt64 {
			return nil, fmt.Errorf("%w: integer overflow", errCBOR)
		}

		return int64(arg), nil
	case 1:
		if arg > math.MaxInt64 {
			return nil, fmt.Errorf("%w: integer overflow", errCBOR)
		}

		return -1 - int64(arg), nil
	case 2:
		b, err := d.bytes(arg)
		if err != nil {
			return nil, err
		}

		return append([]byte(nil), b...), nil
	case 3:
		b, err := d.bytes(arg)
		if err != nil {
			return nil, err
		}

		return string(b), nil
	case 4:
		return d.array(arg, depth)
	case 5:
		return d.mapItem(arg, depth)
	case 7:
		return simple(arg)
	default: // 6: tags never appear in WebAuthn structures
		return nil, fmt.Errorf("%w: unsupported major type %d", errCBOR, major)
	}
}

// head reads the initial byte and its argument. Indefinite lengths (31)
// and the reserved additional values (28-30) are rejected.
func (d *cborDecoder) head() (byte, uint64, error) {
	if d.pos >= len(d.data) {
		return 0, 0, fmt.Errorf("%w: unexpected end", errCBOR)
	}

	b := d.data[d.pos]
	d.pos++

	major, info := b>>5, b&0x1f

	if info < 24 {
		return major, uint64(info), nil
	}

	size := 0

	switch info {
	case 24:
		size = 1
	case 25:
		size = 2
	case 26:
		size = 4
	case 27:
		size = 8
	default:
		return 0, 0, fmt.Errorf("%w: indefinite or reserved length", errCBOR)
	}

	if len(d.data)-d.pos < size {
		return 0, 0, fmt.Errorf("%w: unexpected end", errCBOR)
	}

	var buf [8]byte

	copy(buf[8-size:], d.data[d.pos:d.pos+size])
	d.pos += size

	return major, binary.BigEndian.Uint64(buf[:]), nil
}

// left is the number of bytes not yet read.
func (d *cborDecoder) left() uint64 {
	return uint64(len(d.data) - d.pos) //nolint:gosec // G115: pos never exceeds len(data)
}

func (d *cborDecoder) bytes(n uint64) ([]byte, error) {
	if n > d.left() {
		return nil, fmt.Errorf("%w: length beyond input", errCBOR)
	}

	end := d.pos + int(n) //nolint:gosec // G115: n <= left(), so it fits an int
	b := d.data[d.pos:end]
	d.pos = end

	return b, nil
}

func (d *cborDecoder) array(n uint64, depth int) ([]any, error) {
	// Each element takes at least one byte, so a count beyond the bytes
	// left is a lie that would otherwise size a huge allocation.
	if n > d.left() {
		return nil, fmt.Errorf("%w: length beyond input", errCBOR)
	}

	out := make([]any, 0, n)

	for range n {
		v, err := d.item(depth + 1)
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, nil
}

func (d *cborDecoder) mapItem(n uint64, depth int) (map[any]any, error) {
	if n > d.left()/2 {
		return nil, fmt.Errorf("%w: length beyond input", errCBOR)
	}

	out := make(map[any]any, n)

	for range n {
		k, err := d.item(depth + 1)
		if err != nil {
			return nil, err
		}

		switch k.(type) {
		case int64, string, bool:
		default:
			return nil, fmt.Errorf("%w: unsupported map key type", errCBOR)
		}

		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("%w: duplicate map key", errCBOR)
		}

		v, err := d.item(depth + 1)
		if err != nil {
			return nil, err
		}

		out[k] = v
	}

	return out, nil
}

func simple(arg uint64) (any, error) {
	switch arg {
	case 20:
		return false, nil
	case 21:
		return true, nil
	case 22:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unsupported simple value %d", errCBOR, arg)
	}
}
