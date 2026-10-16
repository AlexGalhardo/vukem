package bootstrap

import (
	"example.com/blog/app/providers"

	lumek "github.com/AlexGalhardo/golang-framework"
)

// Providers lists the application providers; lumek make:provider appends here.
func Providers() []lumek.Provider {
	return []lumek.Provider{
		providers.BillingProvider{},
	}
}
