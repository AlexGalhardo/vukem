package webauthn

import (
	"bytes"
	"errors"
	"testing"
)

func TestDecodeCBOR(t *testing.T) {
	t.Parallel()

	v, n, err := decodeCBOR(append(enc(cmap{{"fmt", "none"}, {-2, []byte{1, 2}}, {1, true}}), 0xff))
	if err != nil {
		t.Fatal(err)
	}

	m, _ := v.(map[any]any)
	if m["fmt"] != "none" || !bytes.Equal(m[int64(-2)].([]byte), []byte{1, 2}) || m[int64(1)] != true || n != 16 {
		t.Fatalf("decodeCBOR() = %v, %d", v, n)
	}
}

func TestDecodeCBORRejects(t *testing.T) {
	t.Parallel()

	deep := bytes.Repeat([]byte{0x81}, maxCBORDepth+2)

	tests := map[string][]byte{
		"empty":                {},
		"indefinite map":       {0xbf, 0xff},
		"byte string too long": {0x5a, 0xff, 0xff, 0xff, 0xff},
		"huge array":           {0x9b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		"huge map":             {0xba, 0x7f, 0xff, 0xff, 0xff},
		"truncated head":       {0x19, 0x01},
		"tag":                  {0xc0, 0x00},
		"float":                {0xfa, 0, 0, 0, 0},
		"duplicate key":        enc(cmap{{1, 1}, {1, 2}}),
		"bytes key":            enc(cmap{{[]byte{1}, 1}}),
		"uint overflow":        {0x1b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		"too deep":             append(deep, 0),
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := decodeCBOR(in); !errors.Is(err, errCBOR) {
				t.Fatalf("decodeCBOR(%x) = %v, want errCBOR", in, err)
			}
		})
	}
}

// FuzzDecodeCBOR: the decoder never panics, never reads past its input,
// and decoding exactly the bytes it used gives the same length.
func FuzzDecodeCBOR(f *testing.F) {
	f.Add(enc(cmap{{"fmt", "none"}, {"attStmt", cmap{}}, {"authData", []byte{1, 2, 3}}}))
	f.Add([]byte{0x9f, 0xff})
	f.Add([]byte{0x81, 0x81, 0x81, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, n, err := decodeCBOR(data)
		if err != nil {
			return
		}

		if n <= 0 || n > len(data) {
			t.Fatalf("decodeCBOR() used %d of %d bytes", n, len(data))
		}

		if _, n2, err := decodeCBOR(data[:n]); err != nil || n2 != n {
			t.Fatalf("decodeCBOR(prefix) = %d, %v, want %d, nil", n2, err, n)
		}
	})
}

// FuzzParseAuthData: authenticator data from the client never panics the
// parser, and a parsed credential id stays within the WebAuthn limit.
func FuzzParseAuthData(f *testing.F) {
	a := &authenticator{credID: []byte{1, 2, 3}, flags: flagUP | flagUV | flagED, rpID: testRPID}
	f.Add(append(a.authData(true, enc(cmap{{1, 2}})), enc(cmap{{"credProps", true}})...))
	f.Add(make([]byte, 37))

	f.Fuzz(func(t *testing.T, data []byte) {
		ad, err := parseAuthData(data)
		if err != nil {
			return
		}

		if len(ad.credID) > maxCredentialIDLen || len(ad.rpIDHash) != 32 {
			t.Fatalf("parseAuthData() = %+v", ad)
		}
	})
}
