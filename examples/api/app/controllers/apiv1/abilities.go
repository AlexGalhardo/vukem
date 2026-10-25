package apiv1

// The abilities an API token can be granted. This is the app's ability
// set: add yours here, then gate the routes that need them with
// auth.Ability(acc.API, apiv1.AbilityX) in routes/api.go.
//
// A token only ever carries the abilities the caller asked for, so a new
// ability is refused to every key issued before it existed.
const (
	// AbilityRead reads through the API.
	AbilityRead = "read"
	// AbilityWrite creates, changes and deletes through the API.
	AbilityWrite = "write"
)

// Abilities lists every ability POST /api/v1/tokens may grant. A request
// asking for anything else is refused, so a typo cannot widen a key.
func Abilities() []string { return []string{AbilityRead, AbilityWrite} }
