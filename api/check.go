package api

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/AlexGalhardo/vukem/z"
)

// CheckInputs reports, as an error, the mistakes [Register] would panic with
// at boot for the given input types: an unknown or misplaced `validate`
// rule, an unsupported field type, a malformed [RawBody] field. Every type is
// checked and the errors are joined, each naming its type. Call it from a
// test so a bad tag fails the test instead of the server start:
//
//	func TestInputTypes(t *testing.T) {
//		err := api.CheckInputs(
//			reflect.TypeFor[StoreProduct](),
//			reflect.TypeFor[ListProducts](),
//		)
//		if err != nil {
//			t.Fatal(err)
//		}
//	}
//
// [Register] still panics: this is the same check, callable without
// registering a route. An empty struct (a route without input) is valid.
// A type with `validate` tags that [RegisterWith] replaces with its own
// schema is still checked against its tags, which Register would reject.
func CheckInputs(types ...reflect.Type) error {
	var errs []error

	for _, t := range types {
		if err := checkInput(t); err != nil {
			errs = append(errs, fmt.Errorf("api: input %s: %w", t, err))
		}
	}

	return errors.Join(errs...)
}

func checkInput(t reflect.Type) (err error) {
	if t == nil || t.Kind() != reflect.Struct {
		return errors.New("an input type must be a struct")
	}

	if t.NumField() == 0 {
		return nil
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()

	rawBodyField("CheckInputs", t.String(), t)

	return z.CheckTags(t)
}
