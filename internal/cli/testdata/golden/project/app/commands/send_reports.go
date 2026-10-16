package commands

import (
	"context"
	"fmt"

	vukem "github.com/AlexGalhardo/vukem"
)

// SendReports is the "send-reports" command: vukem send-reports.
func SendReports() vukem.Command {
	return vukem.CommandFunc{
		CommandName: "send-reports",
		Desc:        "Describe what send-reports does",
		Fn: func(ctx context.Context, app *vukem.App, args []string) error {
			fmt.Println("send-reports ran with", args)
			return nil
		},
	}
}
