package z

import (
	"fmt"
	"reflect"
)

// CheckTags builds the tag schema of struct type t, as [For] does, and
// returns the mistake [For] would panic with (an unknown `validate` rule, a
// rule that does not fit the field's type, an unsupported field type) as an
// error. A test calls it, through api.CheckInputs, to find a bad tag before
// the server boots.
func CheckTags(t reflect.Type) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()

	tagCore(t, 0)

	return nil
}
