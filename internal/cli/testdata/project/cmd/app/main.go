package main

import (
	"os"

	"example.com/blog/bootstrap"

	lumek "github.com/AlexGalhardo/golang-framework"
)

func main() { os.Exit(lumek.Main(bootstrap.New)) }
