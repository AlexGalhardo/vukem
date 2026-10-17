package config_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

type MailConfig struct {
	Host     string        `env:"MAIL_HOST"     default:"localhost"`
	Port     int           `env:"MAIL_PORT"     default:"1025"`
	Timeout  time.Duration `env:"MAIL_TIMEOUT"  default:"10s"`
	Password config.Secret `env:"MAIL_PASSWORD"`
}

func ExampleLoad() {
	// Apps pass app.Source, loaded from .env files and the process
	// environment.
	src := config.MapSource(map[string]string{
		"MAIL_HOST":     "smtp.example.com",
		"MAIL_PASSWORD": "hunter2",
	})

	cfg, err := config.Load[MailConfig](src)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(cfg.Host, cfg.Port, cfg.Timeout)
	fmt.Println(cfg.Password, cfg.Password.Reveal() == "hunter2")
	// Output:
	// smtp.example.com 1025 10s
	// [redacted] true
}

func ExampleParseEnv() {
	entries, err := config.ParseEnv(strings.NewReader("# app\nAPP_NAME=Shop\nexport APP_DEBUG=true\n"))
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, e := range entries {
		fmt.Printf("%s=%s\n", e.Key, e.Value)
	}
	// Output:
	// APP_NAME=Shop
	// APP_DEBUG=true
}
