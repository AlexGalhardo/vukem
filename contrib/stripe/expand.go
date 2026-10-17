package stripe

import (
	"bytes"
	"encoding/json"
	"strings"
)

// decodeCollapsed unmarshals b into v after replacing every expanded object
// at the dotted paths ("transfer_data.destination") with its "id", so a
// string field decodes both "cus_1" and the object expand[] returns.
func decodeCollapsed(b []byte, v any, paths ...string) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return json.Unmarshal(b, v)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}

	changed := false

	for _, p := range paths {
		c, err := collapse(m, strings.Split(p, "."))
		if err != nil {
			return err
		}

		changed = changed || c
	}

	if changed {
		var err error
		if b, err = json.Marshal(m); err != nil {
			return err
		}
	}

	return json.Unmarshal(b, v)
}

func collapse(m map[string]json.RawMessage, path []string) (bool, error) {
	raw, ok := m[path[0]]

	raw = bytes.TrimSpace(raw)
	if !ok || len(raw) == 0 || raw[0] != '{' {
		return false, nil
	}

	if len(path) == 1 {
		var obj struct {
			ID string `json:"id"`
		}

		if err := json.Unmarshal(raw, &obj); err != nil {
			return false, err
		}

		id, err := json.Marshal(obj.ID)
		if err != nil {
			return false, err
		}

		m[path[0]] = id

		return true, nil
	}

	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return false, err
	}

	changed, err := collapse(nested, path[1:])
	if err != nil || !changed {
		return false, err
	}

	if m[path[0]], err = json.Marshal(nested); err != nil {
		return false, err
	}

	return true, nil
}

// UnmarshalJSON decodes expanded customer, latest_charge, on_behalf_of and
// transfer_data.destination as their ids.
func (p *PaymentIntent) UnmarshalJSON(b []byte) error {
	type plain PaymentIntent

	return decodeCollapsed(b, (*plain)(p), "customer", "latest_charge", "on_behalf_of", "transfer_data.destination")
}

// UnmarshalJSON decodes expanded customer, subscription and payment_intent
// as their ids.
func (s *CheckoutSession) UnmarshalJSON(b []byte) error {
	type plain CheckoutSession

	return decodeCollapsed(b, (*plain)(s), "customer", "subscription", "payment_intent")
}

// UnmarshalJSON decodes an expanded customer as its id.
func (s *Subscription) UnmarshalJSON(b []byte) error {
	type plain Subscription

	return decodeCollapsed(b, (*plain)(s), "customer")
}

// UnmarshalJSON decodes an expanded price.product as its id.
func (s *SubItem) UnmarshalJSON(b []byte) error {
	type plain SubItem

	return decodeCollapsed(b, (*plain)(s), "price.product")
}

// UnmarshalJSON decodes expanded destination, destination_payment and
// source_transaction as their ids.
func (t *Transfer) UnmarshalJSON(b []byte) error {
	type plain Transfer

	return decodeCollapsed(b, (*plain)(t), "destination", "destination_payment", "source_transaction")
}
