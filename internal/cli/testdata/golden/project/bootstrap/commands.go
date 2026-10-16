package bootstrap

import (
	"example.com/blog/app/commands"
	vukem "github.com/AlexGalhardo/vukem"
)

// Commands lists the application commands; vukem make:command appends here.
func Commands() []vukem.Command {
	return []vukem.Command{
		commands.SendReports(),
	}
}
