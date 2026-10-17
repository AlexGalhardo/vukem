package errs_test

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/AlexGalhardo/vukem/errs"
)

func Example() {
	// Handlers return errors; the central error handler maps them to a
	// response with this status.
	notFound := errs.Wrap(404, fs.ErrNotExist)
	fmt.Println(errs.Status(notFound), notFound.Text())
	fmt.Println(errors.Is(notFound, fs.ErrNotExist))

	fmt.Println(errs.Status(errors.New("boom")))
	// Output:
	// 404 Not Found
	// true
	// 500
}

func ExampleNewValidation() {
	v := errs.NewValidation().
		Add("email", "must be a valid email").
		Add("password", "must be at least 12 characters")

	fmt.Println(errs.Status(v), v.First("email"))
	// Output:
	// 422 must be a valid email
}
