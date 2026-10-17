package s3

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		region    string
		pathStyle bool
		existing  bool
		location  string
	}{
		{name: "path style", region: testRegion, pathStyle: true, location: testRegion},
		{name: "virtual hosted", region: testRegion, location: testRegion},
		{name: "us-east-1 sends no location", region: "us-east-1", pathStyle: true, location: ""},
		{name: "auto (R2) sends no location", region: "auto", pathStyle: true, location: ""},
		{name: "already owned is idempotent", region: testRegion, pathStyle: true, existing: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeS3()
			f.region, f.noBucket = tt.region, !tt.existing
			srv := httptest.NewServer(f)
			t.Cleanup(srv.Close)

			cfg := testConfig(srv.URL)
			cfg.Region, cfg.UsePathStyle = tt.region, tt.pathStyle

			d, err := New(cfg, WithHTTPClient(hostDialer(srv)))
			if err != nil {
				t.Fatal(err)
			}

			for range 2 {
				if err := d.CreateBucket(t.Context()); err != nil {
					t.Fatalf("CreateBucket() = %v, want nil", err)
				}
			}

			f.mu.Lock()
			location := f.location
			f.mu.Unlock()

			if location != tt.location {
				t.Errorf("CreateBucket() LocationConstraint = %q, want %q", location, tt.location)
			}

			if err := d.Put(t.Context(), "a.txt", strings.NewReader("hi")); err != nil {
				t.Errorf("Put() after CreateBucket = %v", err)
			}
		})
	}
}

// hostDialer is a client that dials srv whatever the host, so
// bucket.<host> (virtual-hosted style) reaches the fake.
func hostDialer(srv *httptest.Server) *http.Client {
	var dialer net.Dialer

	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}}
}

func TestCreateBucketTakenByAnotherAccount(t *testing.T) {
	t.Parallel()

	d, f := newTestDisk(t)
	f.mu.Lock()
	f.foreign = true
	f.mu.Unlock()

	err := d.CreateBucket(t.Context())

	var s3err *Error
	if !errors.As(err, &s3err) || s3err.Code != "BucketAlreadyExists" {
		t.Fatalf("CreateBucket() = %v, want *Error BucketAlreadyExists", err)
	}
}
