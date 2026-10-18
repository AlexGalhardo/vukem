// Command app runs blog: the HTTP server by default, or a command such as
// migrate, route:list or about (vukem <command> delegates here).
package main

import (
	"os"

	vukem "github.com/AlexGalhardo/vukem"

	"example.com/blog/bootstrap"
)

func main() { os.Exit(vukem.Main(bootstrap.New)) }
