// Package finance holds the personal finance rules: accounts, categories,
// transactions, budgets, the dashboard numbers and the CSV import. Money is
// int64 cents in the ISO 4217 currency of the workspace, never a float.
// Every query of a domain table goes through workspaces.Scoped.
package finance

import (
	"errors"
	"strconv"
	"strings"
)

// ErrAmount is the answer to text that is not an amount.
var ErrAmount = errors.New("finance: not a valid amount")

// ParseAmount reads a decimal amount ("12.34", "12,34", "1,234.56",
// "1.234,56", "-$5", "(5.00)") into cents. A single separator is the decimal
// one; with two kinds the last one is. More than two decimals is an error
// rather than a rounding.
func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-") || (strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")"))

	var b strings.Builder

	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			b.WriteRune(r)
		}
	}

	num := b.String()
	if num == "" {
		return 0, ErrAmount
	}

	dec := strings.LastIndexAny(num, ".,")
	whole, frac := num, ""

	if dec >= 0 {
		whole, frac = num[:dec], num[dec+1:]
		// "1.234.567" has no decimal part: the separator repeats.
		if strings.Count(num, string(num[dec])) > 1 {
			whole, frac = num, ""
		}
	}

	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	if len(frac) > 2 || whole == "" && frac == "" {
		return 0, ErrAmount
	}

	frac += strings.Repeat("0", 2-len(frac))

	if whole == "" {
		whole = "0"
	}

	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || units > (1<<62)/100 {
		return 0, ErrAmount
	}

	cents, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, ErrAmount
	}

	total := units*100 + cents
	if neg {
		total = -total
	}

	return total, nil
}

var symbols = map[string]string{"USD": "$", "EUR": "€", "GBP": "£", "BRL": "R$", "JPY": "¥"}

// FormatAmount renders cents for emails: "$1,234.50", "-R$5.00". Currencies
// without a known symbol are prefixed with their code.
func FormatAmount(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}

	symbol, ok := symbols[currency]
	if !ok {
		symbol = currency + " "
	}

	whole := strconv.FormatInt(cents/100, 10)

	var grouped strings.Builder

	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte(',')
		}

		grouped.WriteRune(r)
	}

	frac := strconv.FormatInt(cents%100, 10)
	if len(frac) < 2 {
		frac = "0" + frac
	}

	return sign + symbol + grouped.String() + "." + frac
}
