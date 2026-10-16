package main

import (
	"os"

	"example.com/blog/bootstrap"

	vukem "github.com/AlexGalhardo/vukem"
)

func main() { os.Exit(vukem.Main(bootstrap.New)) }
