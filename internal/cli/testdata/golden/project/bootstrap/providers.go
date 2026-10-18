package bootstrap

import (
	vukem "github.com/AlexGalhardo/vukem"

	"example.com/blog/app/providers"
)

// Providers lists the application providers; vukem make:provider appends here.
func Providers() []vukem.Provider {
	return []vukem.Provider{
		providers.BillingProvider{},
	}
}
