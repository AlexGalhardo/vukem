package z_test

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/z"
)

type registerInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Age      int    `json:"age"`
}

var registerSchema = z.Struct[registerInput](z.Shape{
	"email":    z.String().Trim().Email(),
	"password": z.String().Min(12),
	"age":      z.Int().Min(18),
})

func Example() {
	in, err := registerSchema.Parse(map[string]any{
		"email":    "  ada@example.com ",
		"password": "correct horse battery",
		"age":      36,
	})
	fmt.Printf("%q %d %v\n", in.Email, in.Age, err)
	// Output:
	// "ada@example.com" 36 <nil>
}

func ExampleAsIssues() {
	_, err := registerSchema.Parse(map[string]any{"email": "nope", "password": "short", "age": 12})

	issues, _ := z.AsIssues(err)
	for _, field := range []string{"email", "password", "age"} {
		fmt.Printf("%s: %s\n", field, issues.First(field))
	}
	// Output:
	// email: The email field must be a valid email address.
	// password: The password field must be at least 12 characters.
	// age: The age field must be at least 18.
}
