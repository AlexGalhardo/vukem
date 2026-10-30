package http3_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qhttp3 "github.com/quic-go/quic-go/http3"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/contrib/http3"
)

func writeCert(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vukem-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	if err := os.WriteFile(
		certFile,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		keyFile,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)

	return certFile, keyFile, pool
}

func TestServeRoundTripAndAltSvc(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := writeCert(t)
	env := map[string]string{
		"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "HOST": "127.0.0.1", "PORT": "0",
		"TLS_CERT_FILE": certFile, "TLS_KEY_FILE": keyFile,
	}

	tcpReady, udpReady := make(chan string, 1), make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	build := func(opts ...vukem.Option) (*vukem.App, error) {
		app, err := vukem.New(
			append(opts, vukem.WithLogger(slog.New(slog.DiscardHandler)), vukem.WithReady(tcpReady))...)
		if err != nil {
			return nil, err
		}

		return app, http3.Serve(app, http3.Options{Addr: "127.0.0.1:0", Ready: udpReady})
	}

	errc := make(chan error, 1)

	go func() { errc <- vukem.RunMain(ctx, []string{"app"}, getenv, build) }()

	tcpAddr, udpAddr := <-tcpReady, <-udpReady

	tr := &qhttp3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	t.Cleanup(func() { tr.Close() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+udpAddr+"/up", http.NoBody)

	res, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	if res.ProtoMajor != 3 || res.StatusCode != http.StatusOK || res.Header.Get("Alt-Svc") != "" {
		t.Errorf("HTTP/3 GET /up = %s %d, Alt-Svc %q", res.Proto, res.StatusCode, res.Header.Get("Alt-Svc"))
	}

	// The TCP server's scheme depends on the core version; Alt-Svc does not.
	req, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+tcpAddr+"/up", http.NoBody)

	tcp := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	if res, err = tcp.Do(req); err != nil {
		req.URL.Scheme = "http"
		res, err = tcp.Do(req)
	}

	if err != nil {
		t.Fatal(err)
	}

	res.Body.Close()

	_, port, _ := net.SplitHostPort(udpAddr)
	if got := res.Header.Get("Alt-Svc"); !strings.Contains(got, `h3=":`+port+`"`) {
		t.Errorf("TCP Alt-Svc = %q, want h3 on port %s", got, port)
	}

	cancel()

	if err := <-errc; err != nil {
		t.Fatalf("RunMain() = %v", err)
	}
}

func TestServeNeedsCertificate(t *testing.T) {
	t.Parallel()

	app, err := vukem.New(vukem.WithSource(config.MapSource(map[string]string{"APP_KEY": config.GenerateKey()})),
		vukem.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}

	if err := http3.Serve(app, http3.Options{}); !errors.Is(err, http3.ErrNoTLS) {
		t.Errorf("Serve() = %v, want ErrNoTLS", err)
	}
}

func TestSecurity_A02_HTTP3OnlyTLS13(t *testing.T) {
	t.Parallel()

	// QUIC is TLS 1.3 by definition (RFC 9001 §4.2); a client capped at
	// TLS 1.2 cannot connect.
	certFile, keyFile, pool := writeCert(t)
	env := map[string]string{
		"APP_KEY": config.GenerateKey(), "HOST": "127.0.0.1", "PORT": "0",
		"TLS_CERT_FILE": certFile, "TLS_KEY_FILE": keyFile,
	}

	udpReady := make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	build := func(opts ...vukem.Option) (*vukem.App, error) {
		app, err := vukem.New(append(opts, vukem.WithLogger(slog.New(slog.DiscardHandler)))...)
		if err != nil {
			return nil, err
		}

		return app, http3.Serve(app, http3.Options{Addr: "127.0.0.1:0", Ready: udpReady})
	}

	errc := make(chan error, 1)

	go func() { errc <- vukem.RunMain(ctx, []string{"app"}, getenv, build) }()

	addr := <-udpReady

	tr := &qhttp3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MaxVersion: tls.VersionTLS12}}
	t.Cleanup(func() { tr.Close() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+addr+"/up", http.NoBody)
	if res, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Do(req); err == nil {
		res.Body.Close()
		t.Error("a TLS 1.2 client got an HTTP/3 answer")
	}

	cancel()

	if err := <-errc; err != nil {
		t.Fatalf("RunMain() = %v", err)
	}
}
