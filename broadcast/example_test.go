package broadcast_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/broadcast"
)

type exPostPublished struct {
	Title string `json:"title"`
}

func (exPostPublished) Channels() []string { return []string{"posts"} }

func Example() {
	b := broadcast.New(broadcast.Config{}) // Broker: redisClient across instances
	defer b.Close()
	if err := b.Public("posts"); err != nil {
		fmt.Println(err)
		return
	}

	// Browsers subscribe through r.Get("/broadcast/sse", b.SSE); here a
	// subscription is opened directly.
	ctx := context.Background()
	sub, err := b.Subscribe(ctx, "posts")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer sub.Close()

	if err := b.Broadcast(ctx, exPostPublished{Title: "Hello"}); err != nil {
		fmt.Println(err)
		return
	}
	msg := <-sub.Messages()
	fmt.Println(msg.Channel, msg.Event, string(msg.Data))

	// A channel without a rule is denied.
	_, err = b.Subscribe(ctx, "private-user.1")
	fmt.Println(err != nil)
	// Output:
	// posts exPostPublished {"title":"Hello"}
	// true
}
