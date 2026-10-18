package providers

import vukem "github.com/AlexGalhardo/vukem"

// BillingProvider registers billing services.
type BillingProvider struct{}

// Register implements vukem.Provider. Do not perform I/O here. Name the
// parameter app with the first service it registers.
func (BillingProvider) Register(_ *vukem.App) error {
	return nil
}
