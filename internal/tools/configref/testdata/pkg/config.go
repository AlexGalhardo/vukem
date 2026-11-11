// Package pkg is a fixture for the configref tests.
package pkg

import (
	"time"

	"example.com/config"
)

// Config is the fixture configuration.
type Config struct {
	// Name is the application name. It shows up in logs, e.g. in the header.
	Name  string `env:"APP_NAME" default:"Demo"`
	Debug bool   `env:"APP_DEBUG"`
	// Key signs cookies.
	Key config.Secret `env:"APP_KEY" default:"hunter2"`
	// Note keeps a | in its text.
	Note   string `env:"APP_NOTE"`
	Plain  string
	Wait   time.Duration `env:"WAIT" default:"5s"`
	hidden string        `env:"HIDDEN_FIELD"`
}

// Nested is another exported struct.
type Nested struct {
	// Port is the port.
	Port int `env:"PORT" default:"3000"`
}

type unexported struct {
	X string `env:"UNEXPORTED_STRUCT"`
}

func read(x interface{ Get(string) string }) {
	_ = x
}

type holder struct{ Source src }

type src struct{}

func (src) Get(string) string { return "" }

func extra(a holder) string {
	return a.Source.Get("EXTRA_VAR")
}
