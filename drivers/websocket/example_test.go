package websocket_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"

	cws "github.com/coder/websocket"

	"github.com/AlexGalhardo/vukem/drivers/websocket"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// An echo endpoint. The handler returns when the client closes the
// connection or the app shuts down.
func ExampleHandler() {
	r := router.New(nil)
	r.Get("/ws", websocket.Handler(func(conn *websocket.Conn) error {
		ctx := conn.Context()

		for {
			msg, err := conn.ReadText(ctx)
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			} else if err != nil {
				return err
			}

			if err := conn.WriteText(ctx, "echo: "+msg); err != nil {
				return err
			}
		}
	}))

	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx := context.Background()

	c, resp, err := cws.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		panic(err)
	}

	if resp.Body != nil {
		_ = resp.Body.Close()
	}

	defer c.CloseNow()

	if err := c.Write(ctx, cws.MessageText, []byte("hi")); err != nil {
		panic(err)
	}

	_, got, err := c.Read(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(got))

	_ = c.Close(cws.StatusNormalClosure, "")
	// Output: echo: hi
}

// Handle upgrades from inside a regular handler, after any checks it needs.
// Cross-origin upgrades are refused unless their origin is allowlisted.
func ExampleHandle() {
	r := router.New(nil)
	r.Get("/chat", func(c *web.Ctx) error {
		return websocket.Handle(c, func(conn *websocket.Conn) error {
			var msg struct {
				Text string `json:"text"`
			}

			if err := conn.ReadJSON(conn.Context(), &msg); err != nil {
				return err
			}

			return conn.WriteJSON(conn.Context(), map[string]string{"text": msg.Text})
		}, websocket.AllowedOrigins("https://app.example.com"))
	})
}
