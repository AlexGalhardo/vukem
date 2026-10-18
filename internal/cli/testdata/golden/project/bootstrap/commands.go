package bootstrap

import (
	vukem "github.com/AlexGalhardo/vukem"

	"example.com/blog/app/commands"
)

// Commands lists the application commands; vukem make:command appends here.
func Commands() []vukem.Command {
	return []vukem.Command{
		commands.SendReports(),
	}
}
