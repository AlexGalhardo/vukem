package bootstrap

import (
	"example.com/blog/app/commands"

	lumek "github.com/AlexGalhardo/golang-framework"
)

// Commands lists the application commands; lumek make:command appends here.
func Commands() []lumek.Command {
	return []lumek.Command{
		commands.SendReports(),
	}
}
