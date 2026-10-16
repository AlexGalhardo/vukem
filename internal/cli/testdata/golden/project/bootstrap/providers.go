package bootstrap

import (
	"example.com/blog/app/providers"
	vukem "github.com/AlexGalhardo/vukem"
)

// Providers lists the application providers; vukem make:provider appends here.
func Providers() []vukem.Provider {
	return []vukem.Provider{
		providers.BillingProvider{},
	}
}
