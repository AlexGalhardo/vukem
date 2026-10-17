package otel

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The provider reads OTEL_* settings from app.Source (process environment
// and .env files) and passes them to the exporter explicitly: the OTLP
// exporter would otherwise read only the process environment. Errors name
// the variable, never its value (headers carry API keys, and the others
// may be pasted next to them).

// signalValue returns OTEL_EXPORTER_OTLP_<SIGNAL>_<name>, else
// OTEL_EXPORTER_OTLP_<name>, and the key it came from.
func signalValue(getenv func(string) string, signal, name string) (string, string) {
	key := "OTEL_EXPORTER_OTLP_" + signal + "_" + name
	if v := getenv(key); v != "" {
		return key, v
	}

	key = "OTEL_EXPORTER_OTLP_" + name

	return key, getenv(key)
}

// traceExporterOptions turns the headers, timeout and compression settings
// into exporter options.
func traceExporterOptions(getenv func(string) string) ([]otlptracehttp.Option, error) {
	var opts []otlptracehttp.Option

	if key, v := signalValue(getenv, "TRACES", "HEADERS"); v != "" {
		headers, err := parseList(v)
		if err != nil {
			// The value is not echoed: headers carry API keys.
			return nil, fmt.Errorf("otel: %s: %w", key, err)
		}

		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	if key, v := signalValue(getenv, "TRACES", "TIMEOUT"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			return nil, fmt.Errorf("otel: %s must be a positive number of milliseconds", key)
		}

		opts = append(opts, otlptracehttp.WithTimeout(time.Duration(ms)*time.Millisecond))
	}

	if key, v := signalValue(getenv, "TRACES", "COMPRESSION"); v != "" {
		switch v {
		case "gzip":
			opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
		case "none":
			opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.NoCompression))
		default:
			return nil, fmt.Errorf("otel: %s must be gzip or none", key)
		}
	}

	return opts, nil
}

// resourceAttributes parses OTEL_RESOURCE_ATTRIBUTES.
func resourceAttributes(getenv func(string) string) ([]attribute.KeyValue, error) {
	v := getenv("OTEL_RESOURCE_ATTRIBUTES")
	if v == "" {
		return nil, nil
	}

	m, err := parseList(v)
	if err != nil {
		return nil, fmt.Errorf("otel: OTEL_RESOURCE_ATTRIBUTES: %w", err)
	}

	attrs := make([]attribute.KeyValue, 0, len(m))
	for k, v := range m {
		attrs = append(attrs, attribute.String(k, v))
	}

	return attrs, nil
}

// parseList parses the "key1=value1,key2=value2" format of the OTLP
// headers and resource attributes variables (values percent-decoded).
func parseList(s string) (map[string]string, error) {
	out := map[string]string{}

	for i, pair := range strings.Split(s, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}

		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)

		if !ok || k == "" {
			return nil, fmt.Errorf("entry %d is not key=value", i+1)
		}

		dk, err := url.PathUnescape(k)
		if err != nil {
			return nil, fmt.Errorf("entry %d: bad percent-encoding", i+1)
		}

		dv, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("entry %d: bad percent-encoding", i+1)
		}

		out[dk] = dv
	}

	return out, nil
}

// samplerFromEnv reads OTEL_TRACES_SAMPLER and OTEL_TRACES_SAMPLER_ARG; it
// returns nil when the sampler is not set.
func samplerFromEnv(getenv func(string) string) (sdktrace.Sampler, error) {
	name := getenv("OTEL_TRACES_SAMPLER")
	if name == "" {
		return nil, nil //nolint:nilnil // nil means "use the default"
	}

	ratio := func() (float64, error) {
		arg := getenv("OTEL_TRACES_SAMPLER_ARG")
		if arg == "" {
			return 1, nil
		}

		r, err := strconv.ParseFloat(arg, 64)
		if err != nil || r < 0 || r > 1 {
			return 0, errors.New("otel: OTEL_TRACES_SAMPLER_ARG must be a ratio between 0 and 1")
		}

		return r, nil
	}

	switch name {
	case "always_on":
		return sdktrace.AlwaysSample(), nil
	case "always_off":
		return sdktrace.NeverSample(), nil
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample()), nil
	case "traceidratio", "parentbased_traceidratio":
		r, err := ratio()
		if err != nil {
			return nil, err
		}

		if name == "traceidratio" {
			return sdktrace.TraceIDRatioBased(r), nil
		}

		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(r)), nil
	default:
		return nil, errors.New(
			"otel: unknown OTEL_TRACES_SAMPLER (always_on, always_off, traceidratio, parentbased_always_on, " +
				"parentbased_always_off or parentbased_traceidratio)",
		)
	}
}
