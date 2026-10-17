package schedule_test

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/schedule"
)

func Example() {
	s := schedule.New(schedule.Options{})
	s.Func("ping", func(context.Context) error { return nil }).EveryFiveMinutes()
	s.Command("reports:send").DailyAt("08:00").WithoutOverlapping()

	for _, t := range s.Tasks() {
		fmt.Println(t.Name(), t.Expression())
	}

	now := time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC)
	for _, t := range s.Due(now) {
		fmt.Println("due at 08:00:", t.Name())
	}
	// Output:
	// ping */5 * * * *
	// reports:send 0 8 * * *
	// due at 08:00: ping
	// due at 08:00: reports:send
}

func ExampleParseCron() {
	c, err := schedule.ParseCron("30 9 * * 1-5")
	if err != nil {
		fmt.Println(err)
		return
	}

	from := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC) // a Saturday
	fmt.Println(c.Next(from).Format(time.RFC1123))
	// Output: Mon, 05 Jan 2026 09:30:00 UTC
}
