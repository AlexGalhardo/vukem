package providers

import lumek "github.com/AlexGalhardo/golang-framework"

// BillingProvider registers billing services.
type BillingProvider struct{}

// Register implements lumek.Provider. Do not perform I/O here.
func (BillingProvider) Register(app *lumek.App) error {
	return nil
}
