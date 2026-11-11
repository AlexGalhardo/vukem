package loadtest

import (
	"strings"
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    Rate
		wantErr bool
	}{
		{"50/s", Rate{50, time.Second}, false},
		{"300/1m", Rate{300, time.Minute}, false},
		{"50", Rate{50, time.Second}, false},
		{"5/m", Rate{5, time.Minute}, false},
		{"0/s", Rate{}, true},
		{"-1/s", Rate{}, true},
		{"x", Rate{}, true},
		{"10/0s", Rate{}, true},
		{"10/", Rate{}, true},
	}

	for _, tt := range tests {
		got, err := ParseRate(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseRate(%q) = %+v, %v; want %+v (err %v)", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestSecurity_A10_BenchRefusesNonLoopbackTarget(t *testing.T) {
	t.Parallel()

	for _, u := range []string{"http://example.com/", "http://10.0.0.5/", "http://169.254.169.254/latest", "http://[2001:db8::1]:8080/"} {
		err := checkTarget(u, nil)
		if err == nil || !strings.Contains(err.Error(), "only loopback hosts are allowed") {
			t.Errorf("checkTarget(%q) = %v, want a loopback refusal", u, err)
		}
	}
}

func TestSecurity_A10_BenchAllowsLoopback(t *testing.T) {
	t.Parallel()

	for _, u := range []string{
		"http://localhost/", "http://localhost:3000/x", "http://app.localhost/", "http://127.0.0.1/",
		"http://127.0.0.1:8080/", "http://[::1]/", "http://[::1]:3000/", "https://LOCALHOST/",
	} {
		if err := checkTarget(u, nil); err != nil {
			t.Errorf("checkTarget(%q) = %v, want accepted", u, err)
		}
	}
}

func TestSecurity_A10_BenchAllowRemoteIsExactHost(t *testing.T) {
	t.Parallel()

	allow := []string{"staging.example.com"}

	for _, u := range []string{"http://staging.example.com/", "https://STAGING.example.com:8443/x"} {
		if err := checkTarget(u, allow); err != nil {
			t.Errorf("checkTarget(%q) = %v, want accepted", u, err)
		}
	}

	for _, u := range []string{
		"http://evil-staging.example.com/", "http://staging.example.com.evil.io/", "http://example.com/",
		"http://x.staging.example.com/",
	} {
		if err := checkTarget(u, allow); err == nil {
			t.Errorf("checkTarget(%q) accepted a host that is not allowed", u)
		}
	}
}

func TestSecurity_A10_BenchRejectsURLCredentials(t *testing.T) {
	t.Parallel()

	err := checkTarget("http://user:pw@127.0.0.1/", nil)
	if err == nil || strings.Contains(err.Error(), "pw") {
		t.Fatalf("err = %v, want a refusal that does not echo the credentials", err)
	}
}

func TestBenchRejectsNonHTTPScheme(t *testing.T) {
	t.Parallel()

	for _, u := range []string{"file:///etc/hosts", "ftp://127.0.0.1/", "gopher://localhost/", "127.0.0.1:3000", ""} {
		if err := checkTarget(u, nil); err == nil {
			t.Errorf("checkTarget(%q) accepted a URL that is not http or https", u)
		}
	}
}

func TestSecurity_A04_BenchEnforcesCaps(t *testing.T) {
	t.Parallel()

	ok := Config{Concurrency: 1, Duration: time.Second}

	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"duration", func(c *Config) { c.Duration = time.Hour + time.Second }},
		{"rate", func(c *Config) { c.Concurrency = 0; c.Rate = Rate{100_001, time.Second} }},
		{"rate per minute", func(c *Config) { c.Concurrency = 0; c.Rate = Rate{6_000_061, time.Minute} }},
		{"concurrency", func(c *Config) { c.Concurrency = 10_001 }},
		{"max workers", func(c *Config) { c.Concurrency = 0; c.Rate = Rate{1, time.Second}; c.MaxWorkers = 10_001 }},
		{"timeout", func(c *Config) { c.Timeout = 5*time.Minute + time.Second }},
		{"no stop condition", func(c *Config) { c.Duration = 0 }},
		{"negative duration", func(c *Config) { c.Duration = -time.Second }},
		{"rate without period", func(c *Config) { c.Concurrency = 0; c.Rate = Rate{Freq: 5} }},
	}

	for _, tt := range tests {
		cfg := ok
		tt.edit(&cfg)

		if err := checkConfig(&cfg); err == nil {
			t.Errorf("%s: checkConfig accepted %+v", tt.name, cfg)
		}
	}

	good := ok
	if err := checkConfig(&good); err != nil {
		t.Fatalf("a valid config was refused: %v", err)
	}

	atCaps := Config{
		Rate:       Rate{100_000, time.Second},
		Duration:   time.Hour,
		MaxWorkers: 10_000,
		Timeout:    5 * time.Minute,
	}
	if err := checkConfig(&atCaps); err != nil {
		t.Fatalf("a config exactly at the caps was refused: %v", err)
	}
}
