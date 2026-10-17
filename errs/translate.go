package errs

import "context"

// Translator returns the message for key in the request locale with
// {name} placeholders filled from params, or false when no catalog has
// key. The i18n middleware installs one bound to the resolved locale.
type Translator func(key string, params map[string]any) (string, bool)

type translatorKey struct{}

// WithTranslator stores t in ctx. Errors built with a message key
// ([NewKey], [Translate]) are translated through it.
func WithTranslator(ctx context.Context, t Translator) context.Context {
	return context.WithValue(ctx, translatorKey{}, t)
}

// TranslatorFrom returns the Translator stored by WithTranslator, or nil.
func TranslatorFrom(ctx context.Context) Translator {
	t, _ := ctx.Value(translatorKey{}).(Translator)
	return t
}

// Translate returns key translated by the Translator of ctx, or fallback
// (the English message) when ctx has none or it does not know key.
func Translate(ctx context.Context, key, fallback string, params map[string]any) string {
	if key == "" {
		return fallback
	}

	if t := TranslatorFrom(ctx); t != nil {
		if s, ok := t(key, params); ok {
			return s
		}
	}

	return fallback
}

// NewKey returns an *HTTPError whose client-safe message is translated
// from key with params ([HTTPError.LocalizedText]); message is the English
// fallback, also used by Error.
func NewKey(status int, key, message string, params map[string]any) *HTTPError {
	return &HTTPError{Status: status, Message: message, Key: key, Params: params, stack: callers(1)}
}

// LocalizedText returns Text translated from Key by the Translator of
// ctx; errors without a Key, or with an untranslated one, return Text.
func (e *HTTPError) LocalizedText(ctx context.Context) string {
	return Translate(ctx, e.Key, e.Text(), e.Params)
}
