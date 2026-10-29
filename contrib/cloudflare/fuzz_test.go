package cloudflare

import "testing"

func FuzzParseRanges(f *testing.F) {
	f.Add(ipsV4)
	f.Add(ipsV6)
	f.Add([]byte("0.0.0.0/0\n"))
	f.Add([]byte("10.1.2.3/8\r\n\n::/0"))

	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := parseRanges(b)
		if err != nil {
			return
		}

		if len(got) == 0 {
			t.Fatal("parseRanges() = empty list, nil error")
		}

		for _, p := range got {
			if !p.IsValid() || p.Bits() == 0 || p != p.Masked() {
				t.Fatalf("parseRanges() returned %s", p)
			}
		}
	})
}
