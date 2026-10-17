// Command app runs api: the HTTP server by default, or a command such
// as migrate, route:list or about (vukem <command> delegates here).
package main

import (
	"os"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/examples/api/bootstrap"
)

func main() { os.Exit(vukem.Main(bootstrap.New)) }
