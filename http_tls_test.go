package vukem

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCert writes a self-signed 127.0.0.1 certificate named cn into dir
// and returns the file paths and a pool trusting it.
func writeCert(t *testing.T, dir, cn string) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
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

	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	for f, b := range map[string]*pem.Block{
		certFile: {Type: "CERTIFICATE", Bytes: der},
		keyFile:  {Type: "EC PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(f, pem.EncodeToMemory(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)

	return certFile, keyFile, pool
}

func TestSecurity_A02_HTTPSServesH2AndRefusesWeakTLS(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := writeCert(t, t.TempDir(), "vukem-test")
	ready := make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	env := map[string]string{
		"APP_KEY": testKey, "PORT": "0", "HOST": "127.0.0.1", "APP_ENV": "production",
		"TLS_CERT_FILE": certFile, "TLS_KEY_FILE": keyFile,
	}
	getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithReady(ready))...)
	}

	errc := make(chan error, 1)

	go func() { errc <- RunMain(ctx, []string{"app"}, getenv, build) }()

	addr := <-ready

	type result struct {
		proto, status int
		alpn          string
	}

	get := func(tr *http.Transport, url string) (result, error) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)

		res, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			return result{}, err
		}
		defer res.Body.Close()

		r := result{proto: res.ProtoMajor, status: res.StatusCode}
		if res.TLS != nil {
			r.alpn = res.TLS.NegotiatedProtocol
		}

		return r, nil
	}

	var h2 http.Protocols
	h2.SetHTTP2(true)

	if res, err := get(
		&http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, Protocols: &h2},
		"https://"+addr+"/up",
	); err != nil ||
		res.proto != 2 ||
		res.alpn != "h2" {
		t.Errorf("h2 GET /up = %v, %v", res, err)
	}

	var h1 http.Protocols
	h1.SetHTTP1(true)

	if res, err := get(
		&http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, Protocols: &h1},
		"https://"+addr+"/up",
	); err != nil ||
		res.proto != 1 ||
		res.status != http.StatusOK {
		t.Errorf("http/1.1 GET /up = %v, %v", res, err)
	}

	old := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS10,
		MaxVersion: tls.VersionTLS11,
	}
	if _, err := get(&http.Transport{TLSClientConfig: old}, "https://"+addr+"/up"); err == nil {
		t.Error("TLS 1.1 handshake succeeded")
	}

	if res, err := get(&http.Transport{}, "http://"+addr+"/up"); err == nil && res.status == http.StatusOK {
		t.Error("plain HTTP served on the TLS port")
	}

	cancel()

	if err := <-errc; err != nil {
		t.Fatalf("RunMain() = %v", err)
	}
}

func TestServerTLSConfigErrors(t *testing.T) {
	t.Parallel()

	certFile, keyFile, _ := writeCert(t, t.TempDir(), "vukem-test")
	weak := &tls.Config{
		MinVersion:     tls.VersionTLS11,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil },
	}

	for _, tc := range []struct {
		name, want string
		env        map[string]string
		opts       []Option
	}{
		{"cert without key", "set both", map[string]string{"TLS_CERT_FILE": certFile}, nil},
		{"h2c with tls", "HTTP_H2C", map[string]string{"TLS_CERT_FILE": certFile, "TLS_KEY_FILE": keyFile, "HTTP_H2C": "true"}, nil},
		{"weak min version", "TLS 1.2", nil, []Option{WithTLSConfig(weak)}},
		{"no certificate", "no certificate", nil, []Option{WithTLSConfig(&tls.Config{})}},
		{"missing file", "tls:", map[string]string{"TLS_CERT_FILE": certFile + "x", "TLS_KEY_FILE": keyFile}, nil},
		{"redirect without tls", "TLS_REDIRECT_ADDR", map[string]string{"TLS_REDIRECT_ADDR": ":0"}, nil},
		{"redirect to http", "https APP_URL", map[string]string{
			"TLS_CERT_FILE": certFile, "TLS_KEY_FILE": keyFile, "TLS_REDIRECT_ADDR": ":0", "APP_URL": "http://example.com",
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"APP_ENV": "production"}
			maps.Copy(env, tc.env)

			app, err := New(append(tc.opts, WithSource(testSource(env)), WithLogger(discardLogger()))...)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := app.httpRunner(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("httpRunner() err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSecurity_A01_TLSRedirectUsesAppURL(t *testing.T) {
	t.Parallel()

	certFile, keyFile, _ := writeCert(t, t.TempDir(), "vukem-test")
	app, err := New(WithSource(testSource(map[string]string{
		"TLS_CERT_FILE":     certFile,
		"TLS_KEY_FILE":      keyFile,
		"TLS_REDIRECT_ADDR": ":0",
		"APP_URL":           "https://example.com:8443/ignored",
	})), WithLogger(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}

	srv, err := app.tlsRedirectServer(true)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://evil.example/login?next=%2F", nil)
	rec := httptest.NewRecorder()
	srv.Server.Handler.ServeHTTP(rec, req)

	if loc := rec.Header().
		Get("Location"); rec.Code != http.StatusPermanentRedirect ||
		loc != "https://example.com:8443/login?next=%2F" {
		t.Errorf("redirect = %d %q", rec.Code, loc)
	}
}

func TestCertReloaderPicksUpNewPair(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certFile, keyFile, _ := writeCert(t, dir, "first")
	r := &certReloader{certFile: certFile, keyFile: keyFile, log: discardLogger()}

	if _, err := r.load(); err != nil {
		t.Fatal(err)
	}

	writeCert(t, dir, "second")

	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(certFile, future, future); err != nil {
		t.Fatal(err)
	}

	r.checked = time.Time{}

	cert, err := r.get(nil)
	if err != nil {
		t.Fatal(err)
	}

	if leaf, _ := x509.ParseCertificate(cert.Certificate[0]); leaf.Subject.CommonName != "second" {
		t.Errorf("served %q after the files changed, want second", leaf.Subject.CommonName)
	}

	// A broken pair keeps the previous certificate.
	if err := os.WriteFile(keyFile, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	later := future.Add(time.Minute)
	if err := os.Chtimes(keyFile, later, later); err != nil {
		t.Fatal(err)
	}

	r.checked = time.Time{}

	if again, err := r.get(nil); err != nil || again != cert {
		t.Errorf("broken reload = %v, %v; want the previous certificate", again, err)
	}
}
