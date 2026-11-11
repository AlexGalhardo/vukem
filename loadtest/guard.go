package loadtest

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Ceilings on a run: a load generator pointed at the wrong place is a denial-of-service tool, so the size of a run
// is bounded even for a caller that asks for more.
const (
	maxDuration    = time.Hour
	maxRate        = 100_000 // requests per second
	maxConcurrency = 10_000
	maxWorkers     = 10_000
	maxTimeout     = 5 * time.Minute
	// maxRequests is what the highest rate can send in the longest run.
	maxRequests = maxRate * uint64(maxDuration/time.Second)
)

// checkConfig validates the caps. A zero Duration needs Requests, so a run always has an end. Zero Timeout,
// MaxWorkers and Concurrency mean their defaults and pass.
func checkConfig(cfg *Config) error {
	switch {
	case cfg.Duration < 0 || cfg.Duration > maxDuration:
		return fmt.Errorf("loadtest: duration must be at most %s", maxDuration)
	case cfg.Requests > maxRequests:
		return fmt.Errorf("loadtest: requests must be at most %d", maxRequests)
	case cfg.Duration == 0 && cfg.Requests == 0:
		return errors.New("loadtest: set a duration or a request count")
	case cfg.Concurrency < 0 || cfg.Concurrency > maxConcurrency:
		return fmt.Errorf("loadtest: concurrency must be between 0 and %d", maxConcurrency)
	case cfg.MaxWorkers < 0 || cfg.MaxWorkers > maxWorkers:
		return fmt.Errorf("loadtest: max workers must be between 0 and %d", maxWorkers)
	case cfg.Timeout < 0 || cfg.Timeout > maxTimeout:
		return fmt.Errorf("loadtest: timeout must be at most %s", maxTimeout)
	case cfg.Connections < 0:
		return errors.New("loadtest: connections cannot be negative")
	}

	if cfg.Rate.Freq != 0 || cfg.Rate.Per != 0 {
		return checkRate(cfg.Rate)
	}

	return nil
}

func checkRate(r Rate) error {
	if r.Freq < 1 || r.Per <= 0 {
		return errors.New("loadtest: a rate needs a positive count and period")
	}

	if r.perSecond() > maxRate {
		return fmt.Errorf("loadtest: rate must be at most %d requests per second", maxRate)
	}

	return nil
}

// checkTarget accepts only http and https URLs without credentials whose host is a loopback address or one of the
// allow-listed hosts (exact match, ignoring case and port). It runs for every request, not once, because a Targeter
// may change the host.
func checkTarget(rawURL string, allow []string) error {
	_, err := parseTarget(rawURL, allow)

	return err
}

func parseTarget(rawURL string, allow []string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("loadtest: the target is not a valid URL")
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("loadtest: the target must be an http or https URL")
	}

	if u.User != nil {
		return nil, errors.New("loadtest: the target URL must not carry credentials")
	}

	host := u.Hostname()
	if host == "" {
		return nil, errors.New("loadtest: the target URL has no host")
	}

	if loopbackHost(host) {
		return u, nil
	}

	for _, a := range allow {
		if strings.EqualFold(a, host) {
			return u, nil
		}
	}

	return nil, fmt.Errorf(
		"loadtest: refusing to target %q: only loopback hosts are allowed (pass --allow-remote %s if you own it)",
		host, host)
}

// loopbackHost reports localhost, *.localhost and loopback IP literals.
func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}

	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}

	ip, err := netip.ParseAddr(host)

	return err == nil && ip.Unmap().IsLoopback()
}
