package vukem

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/crypt"
	"github.com/AlexGalhardo/vukem/logging"
)

// checkBootConfig runs every boot check on the loaded configuration and
// returns all failures together (errors.Join), so a misconfigured deploy
// shows each problem at once. It also normalizes cfg.ClientIPHeader.
func checkBootConfig(
	cfg *Config,
	src *config.Source,
	o *options,
) (config.Key, []*net.IPNet, []config.Key, error) {
	var errs []error

	if err := checkFlags(o.flags, src); err != nil {
		errs = append(errs, err)
	}

	key, err := checkConfig(cfg, src.EnvSet(), o.kind == KindWarmup)
	if err != nil {
		errs = append(errs, err)
	}

	proxies, err := parseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		errs = append(errs, err)
	} else {
		proxies = append(proxies, o.proxies...)

		// Checked only when TRUSTED_PROXIES parsed: an invalid entry is
		// already reported above.
		if cfg.TrustForwardedProto && len(proxies) == 0 {
			errs = append(errs, errors.New("vukem: TRUST_FORWARDED_PROTO requires TRUSTED_PROXIES"))
		}

		if cfg.ClientIPHeader, err = clientIPHeader(cfg.ClientIPHeader, len(proxies) > 0); err != nil {
			errs = append(errs, err)
		}
	}

	if err := cfg.Analytics.Validate(); err != nil {
		errs = append(errs, err)
	}

	if err := cfg.IndexNow.Validate(cfg.URL); err != nil {
		errs = append(errs, err)
	}

	if cfg.HSTSForce && !strings.HasPrefix(cfg.URL, "https://") {
		errs = append(errs, errors.New("vukem: HSTS_FORCE requires an https APP_URL"))
	}

	if cfg.MaxHeaderBytes < minMaxHeaderBytes || cfg.MaxHeaderBytes > http.DefaultMaxHeaderBytes {
		errs = append(errs, fmt.Errorf("vukem: HTTP_MAX_HEADER_BYTES %d must be between %d and %d",
			cfg.MaxHeaderBytes, minMaxHeaderBytes, http.DefaultMaxHeaderBytes))
	}

	if cfg.RateLimitIPv6Prefix < 1 || cfg.RateLimitIPv6Prefix > 128 {
		errs = append(errs, fmt.Errorf("vukem: RATE_LIMIT_IPV6_PREFIX %d must be between 1 and 128",
			cfg.RateLimitIPv6Prefix))
	}

	previous, err := parsePreviousKeys(cfg.PreviousKeys)
	if err != nil {
		errs = append(errs, err)
	}

	if err := errors.Join(errs...); err != nil {
		return config.Key{}, nil, nil, err
	}

	return key, proxies, previous, nil
}

// checkConfig enforces secure defaults at boot (fail closed). envSet
// reports whether APP_ENV was given explicitly (config.Source.EnvSet);
// warmup whether the app is built in KindWarmup.
func checkConfig(cfg *Config, envSet, warmup bool) (config.Key, error) {
	var errs []error

	// A missing key is allowed only with an explicit APP_ENV=development or
	// test (a forgotten APP_ENV in production must not boot keyless); every
	// key-derived feature then fails closed (Key.Derive panics, Crypt is nil,
	// cookie sessions refuse to start). A warmup app (codegen) never boots
	// providers or serves, so it tolerates a missing key in any env; routes
	// that need the key at build time still fail closed. A malformed key is
	// always an error.
	key, err := config.ParseKey(cfg.Key)
	keyOptional := warmup || (envSet && !cfg.IsProduction())

	if err != nil && (!errors.Is(err, config.ErrKeyMissing) || !keyOptional) {
		errs = append(errs, err)
	}

	// Any other value would silently skip every production check below.
	switch cfg.Env {
	case config.Development, config.Test, config.Production:
	default:
		errs = append(errs, fmt.Errorf("vukem: APP_ENV %q must be development, test or production", cfg.Env))
	}

	if cfg.IsProduction() && cfg.Debug {
		errs = append(errs, errors.New("vukem: APP_DEBUG must be false when APP_ENV=production"))
	}

	if cfg.IsProduction() && cfg.Devtools {
		errs = append(errs, errors.New("vukem: devtools (WithDevtools, DEVTOOLS) must be off when APP_ENV=production"))
	}

	if cfg.Port < 0 || cfg.Port > 65535 {
		errs = append(errs, fmt.Errorf("vukem: PORT %d is out of range", cfg.Port))
	}

	return key, errors.Join(errs...)
}

// parseTrustedProxies parses TRUSTED_PROXIES: comma-separated CIDRs or
// bare IPs (a single host). Empty entries are skipped; anything else that
// does not parse refuses the boot, since a dropped entry would silently
// change which client IPs rate limits and logs see.
func parseTrustedProxies(s string) ([]*net.IPNet, error) {
	var nets []*net.IPNet

	for entry := range strings.SplitSeq(s, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}

		if _, n, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, n)
			continue
		}

		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("vukem: TRUSTED_PROXIES entry %q is not an IP or CIDR", entry)
		}

		bits := 128
		if ip4 := ip.To4(); ip4 != nil {
			ip, bits = ip4, 32
		}

		nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}

	return nets, nil
}

// headerToken matches an HTTP field name (RFC 9110 token).
var headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// clientIPHeader validates CLIENT_IP_HEADER and returns it canonicalized.
// List headers are refused: their leftmost entries come from the client.
func clientIPHeader(name string, haveProxies bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}

	if !headerToken.MatchString(name) {
		return "", fmt.Errorf("vukem: CLIENT_IP_HEADER %q is not a valid header name", name)
	}

	name = http.CanonicalHeaderKey(name)
	if name == "X-Forwarded-For" || name == "Forwarded" {
		return "", fmt.Errorf("vukem: CLIENT_IP_HEADER %q is a list; leave it empty to use X-Forwarded-For", name)
	}

	if !haveProxies {
		return "", errors.New("vukem: CLIENT_IP_HEADER requires TRUSTED_PROXIES")
	}

	return name, nil
}

// parsePreviousKeys decodes APP_PREVIOUS_KEYS (comma-separated APP_KEY
// values; blanks are skipped). A malformed entry is a boot error.
func parsePreviousKeys(previous config.Secret) ([]config.Key, error) {
	var keys []config.Key

	for i, s := range strings.Split(previous.Reveal(), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}

		k, err := config.ParseKey(config.NewSecret(s))
		if err != nil {
			return nil, fmt.Errorf("vukem: APP_PREVIOUS_KEYS entry %d: %w", i+1, err)
		}

		keys = append(keys, k)
	}

	return keys, nil
}

// newCrypt builds app.Crypt: the "crypt" key derived from APP_KEY encrypts,
// keys derived from APP_PREVIOUS_KEYS only decrypt. Without APP_KEY it
// returns nil (see crypt.ErrNoKey).
func newCrypt(key config.Key, previous []config.Key) (*crypt.Encrypter, error) {
	if key.IsZero() {
		return nil, nil
	}

	return crypt.New(key.Derive("crypt", crypt.KeySize), derivePrevious(previous, "crypt", crypt.KeySize)...)
}

func derivePrevious(previous []config.Key, purpose string, n int) [][]byte {
	if len(previous) == 0 {
		return nil
	}

	out := make([][]byte, len(previous))
	for i, k := range previous {
		out[i] = k.Derive(purpose, n)
	}

	return out
}

// logLevel is LOG_LEVEL (default info), lowered to debug by APP_DEBUG.
func logLevel(cfg *Config) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}

	if cfg.Debug {
		level = min(level, slog.LevelDebug)
	}

	return level
}

// logDedupWindow is LOG_DEDUP_WINDOW: empty means
// logging.DefaultDedupWindow in production and off elsewhere.
func logDedupWindow(cfg *Config) (time.Duration, error) {
	if cfg.LogDedupWindow == "" {
		if cfg.IsProduction() {
			return logging.DefaultDedupWindow, nil
		}

		return 0, nil
	}

	d, err := time.ParseDuration(cfg.LogDedupWindow)
	if err != nil || d < 0 {
		return 0, fmt.Errorf(
			"vukem: LOG_DEDUP_WINDOW %q: want a duration such as 1m, or 0 to disable",
			cfg.LogDedupWindow,
		)
	}

	return d, nil
}

func newLogger(cfg *Config, out io.Writer) *slog.Logger {
	hopts := &slog.HandlerOptions{Level: logLevel(cfg)}

	format := strings.ToLower(cfg.LogFormat)
	if format == "" {
		format = "text"
		if cfg.IsProduction() {
			format = "json"
		}
	}

	if format == "json" {
		return slog.New(slog.NewJSONHandler(out, hopts))
	}

	return slog.New(slog.NewTextHandler(out, hopts))
}
