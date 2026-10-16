package commands

import (
	"context"
	"fmt"

	lumek "github.com/AlexGalhardo/golang-framework"
)

// SendReports is the "send-reports" command: lumek send-reports.
func SendReports() lumek.Command {
	return lumek.CommandFunc{
		CommandName: "send-reports",
		Desc:        "Describe what send-reports does",
		Fn: func(ctx context.Context, app *lumek.App, args []string) error {
			fmt.Println("send-reports ran with", args)
			return nil
		},
	}
}
