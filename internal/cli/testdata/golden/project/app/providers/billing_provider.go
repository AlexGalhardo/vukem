package providers

import vukem "github.com/AlexGalhardo/vukem"

// BillingProvider registers billing services.
type BillingProvider struct{}

// Register implements vukem.Provider. Do not perform I/O here.
func (BillingProvider) Register(app *vukem.App) error {
	return nil
}
