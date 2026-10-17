package devtools_test

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/devtools"
)

func ExampleNormalizeSQL() {
	// Literals never reach the dashboard; the shape also groups N+1 queries.
	fmt.Println(devtools.NormalizeSQL("SELECT * FROM posts WHERE user_id = 42 AND title = 'secret'"))
	fmt.Println(devtools.NormalizeSQL("SELECT * FROM users WHERE id IN ($1, $2, $3)"))
	// Output:
	// SELECT * FROM posts WHERE user_id = ? AND title = ?
	// SELECT * FROM users WHERE id IN (?...)
}

func ExampleRedactArgs() {
	fmt.Println(devtools.RedactArgs([]any{42, "hunter2", []byte("raw")}))
	// Output: [int string(len=7) bytes(len=3)]
}
