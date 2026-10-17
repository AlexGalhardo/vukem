package httpclient_test

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/AlexGalhardo/vukem/httpclient"
)

func Example() {
	// Fetch a URL a user typed in (a webhook target, an avatar link).
	// Internal destinations fail with ErrBlockedAddress.
	client := httpclient.Safe(httpclient.Options{Timeout: 5 * time.Second})
	resp, err := client.Get("https://example.com/avatar.png")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	avatar, err := io.ReadAll(resp.Body) // fails past Options.MaxBody
	fmt.Println(len(avatar), err)
}

func ExampleGet() {
	resp, err := httpclient.Get(context.Background(), "https://example.com/feed.xml")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	fmt.Println(resp.Status)
}

func ExampleAllowed() {
	for _, ip := range []string{"93.184.216.34", "127.0.0.1", "10.0.0.5", "169.254.169.254"} {
		fmt.Println(ip, httpclient.Allowed(netip.MustParseAddr(ip), nil))
	}
	// Output:
	// 93.184.216.34 true
	// 127.0.0.1 false
	// 10.0.0.5 false
	// 169.254.169.254 false
}
