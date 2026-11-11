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

type payment interface{ isPayment() }

type card struct {
	Method string `json:"method"`
	Number string `json:"number"`
}

type pix struct {
	Method string `json:"method"`
	Key    string `json:"key"`
}

func (card) isPayment() {}
func (pix) isPayment()  {}

func ExampleDiscriminatedUnion() {
	schema := z.DiscriminatedUnion[payment]("method", map[string]z.AnySchema{
		"card": z.Struct[card](z.Shape{"method": z.Literal("card"), "number": z.String().Len(16)}),
		"pix":  z.Struct[pix](z.Shape{"method": z.Literal("pix"), "key": z.String().Min(1)}),
	})

	p, err := schema.Parse(map[string]any{"method": "pix", "key": "ada@example.com"})
	if v, ok := p.(pix); ok {
		fmt.Println("pix", v.Key, err)
	}

	_, err = schema.Parse(map[string]any{"method": "card", "number": "12"})
	issues, _ := z.AsIssues(err)
	fmt.Println(issues.First("number"))

	_, err = schema.Parse(map[string]any{"method": "cash"})
	issues, _ = z.AsIssues(err)
	fmt.Println(issues.First("method"))
	// Output:
	// pix ada@example.com <nil>
	// The number field must be 16 characters.
	// The selected method is invalid.
}

type createPost struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type updatePost struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

func ExamplePartial() {
	create := z.Struct[createPost](z.Shape{
		"title": z.String().Trim().Min(3).Max(120),
		"body":  z.String().Trim().Max(10_000),
	})
	update := z.Partial[updatePost](create)

	got, err := update.Parse(map[string]any{"title": "  Hello "})
	fmt.Println(*got.Title, got.Body == nil, err)

	_, err = update.Parse(map[string]any{"title": "Hi"})
	issues, _ := z.AsIssues(err)
	fmt.Println(issues.First("title"))
	// Output:
	// Hello true <nil>
	// The title field must be at least 3 characters.
}
