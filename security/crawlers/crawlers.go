// Package crawlers verifies that a request claiming to be a search engine
// crawler (Googlebot, Bingbot, Applebot, YandexBot) really comes from it,
// the way the engines document: a reverse DNS lookup of the client IP must
// give a host name under the engine's domain, and a forward lookup of that
// name must give the IP back. A User-Agent alone proves nothing.
//
//	v := crawlers.New(crawlers.Config{})
//	if name, ok := v.Verify(r.Context(), web.ClientIP(r)); ok {
//		// name is "google", "bing", "apple" or "yandex"
//	}
//
// Lookups run with a timeout and their results are cached per IP, so a
// flood of requests from one address costs one pair of DNS queries per
// TTL. Every failure (timeout, no PTR record, a name outside the domains,
// a forward lookup without the IP) is "not verified": it fails closed.
package crawlers

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Defaults for Config.
const (
	DefaultTimeout    = 2 * time.Second
	DefaultTTL        = time.Hour
	DefaultErrorTTL   = time.Minute
	DefaultMaxEntries = 10_000
)

// maxNames bounds the PTR names checked per IP, so a hostile reverse zone
// cannot make one verification run many forward lookups.
const maxNames = 5

// Crawler is a search engine and the domains its crawler hosts live under.
type Crawler struct {
	// Name identifies the crawler in Verify's result ("google").
	Name string
	// Domains are the reverse-DNS domains, without a leading dot
	// ("googlebot.com"); a host matches the domain itself or a subdomain.
	Domains []string
}

// Known returns the crawlers verified by default, with the domains each
// engine documents for reverse-DNS verification.
func Known() []Crawler {
	return []Crawler{
		{Name: "google", Domains: []string{"googlebot.com", "google.com", "googleusercontent.com"}},
		{Name: "bing", Domains: []string{"search.msn.com"}},
		{Name: "apple", Domains: []string{"applebot.apple.com"}},
		{Name: "yandex", Domains: []string{"yandex.ru", "yandex.net", "yandex.com"}},
	}
}

// Resolver is the DNS client Verify uses; *net.Resolver implements it.
type Resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Config configures a Verifier. The zero value verifies Known() with
// net.DefaultResolver and the Default* limits.
type Config struct {
	// Crawlers to accept; nil uses Known().
	Crawlers []Crawler
	// Resolver does the lookups; nil uses net.DefaultResolver.
	Resolver Resolver
	// Timeout bounds both lookups of one verification (DefaultTimeout).
	Timeout time.Duration
	// TTL is how long a verified or rejected IP is cached (DefaultTTL).
	TTL time.Duration
	// ErrorTTL is how long a DNS error is cached (DefaultErrorTTL), shorter
	// so a resolver hiccup does not shut a real crawler out for long.
	ErrorTTL time.Duration
	// MaxEntries caps the cache (DefaultMaxEntries).
	MaxEntries int
	// Now is the clock (tests); nil uses time.Now.
	Now func() time.Time
}

// Verifier checks crawler IPs. It is safe for concurrent use.
type Verifier struct {
	cfg Config

	mu    sync.Mutex
	cache map[netip.Addr]entry
}

type entry struct {
	name    string
	expires time.Time
}

// New returns a Verifier for cfg.
func New(cfg Config) *Verifier {
	if cfg.Crawlers == nil {
		cfg.Crawlers = Known()
	}

	if cfg.Resolver == nil {
		cfg.Resolver = net.DefaultResolver
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}

	if cfg.TTL <= 0 {
		cfg.TTL = DefaultTTL
	}

	if cfg.ErrorTTL <= 0 {
		cfg.ErrorTTL = DefaultErrorTTL
	}

	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = DefaultMaxEntries
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Verifier{cfg: cfg, cache: map[netip.Addr]entry{}}
}

// Verify reports whether ip (as web.ClientIP returns it) belongs to one of
// the crawlers, and which one. An invalid IP is not verified.
func (v *Verifier) Verify(ctx context.Context, ip string) (string, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", false
	}

	addr = addr.Unmap()
	now := v.cfg.Now()

	v.mu.Lock()
	e, ok := v.cache[addr]
	v.mu.Unlock()

	if ok && now.Before(e.expires) {
		return e.name, e.name != ""
	}

	name, err := v.lookup(ctx, addr)

	ttl := v.cfg.TTL
	if err != nil {
		ttl = v.cfg.ErrorTTL
	}

	v.store(addr, entry{name: name, expires: now.Add(ttl)}, now)

	return name, name != ""
}

func (v *Verifier) lookup(ctx context.Context, addr netip.Addr) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Timeout)
	defer cancel()

	names, err := v.cfg.Resolver.LookupAddr(ctx, addr.String())
	if err != nil {
		return "", err
	}

	if len(names) > maxNames {
		names = names[:maxNames]
	}

	var lastErr error

	for _, host := range names {
		host = strings.ToLower(strings.TrimSuffix(host, "."))

		crawler := v.match(host)
		if crawler == "" {
			continue
		}

		ips, err := v.cfg.Resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			lastErr = err
			continue
		}

		for _, got := range ips {
			if got.Unmap() == addr {
				return crawler, nil
			}
		}
	}

	return "", lastErr
}

func (v *Verifier) match(host string) string {
	for _, c := range v.cfg.Crawlers {
		for _, d := range c.Domains {
			if strings.HasSuffix(host, "."+strings.ToLower(d)) {
				return c.Name
			}
		}
	}

	return ""
}

// store caches e, first dropping expired entries and then, if the cache is
// still full, an arbitrary one.
func (v *Verifier) store(addr netip.Addr, e entry, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if len(v.cache) >= v.cfg.MaxEntries {
		for k, old := range v.cache {
			if !now.Before(old.expires) {
				delete(v.cache, k)
			}
		}

		for k := range v.cache {
			if len(v.cache) < v.cfg.MaxEntries {
				break
			}

			delete(v.cache, k)
		}
	}

	v.cache[addr] = e
}
