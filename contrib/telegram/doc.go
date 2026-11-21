// Package telegram runs a Telegram bot: it receives updates by webhook or by
// long polling, routes commands, messages and callback queries to handlers,
// and keeps conversation state between updates. It is the receiving side;
// the logging/telegram handler in the core module only sends log lines and
// is unchanged.
//
//	bot, err := telegram.New(telegram.Config{Token: config.NewSecret(os.Getenv("TELEGRAM_BOT_TOKEN"))})
//	r := telegram.NewRouter()
//	r.Use(telegram.Conversations(telegram.NewMemoryStore(), nil))
//	r.Command("start", func(ctx context.Context, c *telegram.Context) error {
//		return c.Reply(ctx, "Hello, "+c.User().FirstName)
//	})
//
//	// Webhook: mount on the Vukem router.
//	h, err := bot.WebhookHandler(r, config.NewSecret(secret), telegram.WebhookOptions{})
//	app.Router.Handle("POST /telegram/webhook", h)
//
//	// Or long polling, for development and for apps without a public URL.
//	err = bot.Poll(ctx, r, telegram.PollOptions{})
//
// The bot token is a config.Secret: it is placed in the request URL as the
// Bot API requires, and removed from every error and log line this package
// produces. The webhook secret token is compared in constant time and is
// required; a handler without one is not built.
package telegram
