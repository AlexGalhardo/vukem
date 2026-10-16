package totp_test

import (
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/auth/totp"
)

func Example() {
	secret, _ := totp.GenerateSecret()
	uri := totp.URI("Vukem Shop", "ana@example.com", secret, totp.Options{})
	svg, _ := totp.QR(uri, 200) // show it; store the secret encrypted

	code, _ := totp.Code(secret, time.Now(), totp.Options{}) // what the app shows

	step, ok := totp.Validate(secret, code, time.Now(), 0, totp.Options{})
	fmt.Println(svg != "", ok, step > 0) // store step: the same code is refused next time
	// Output: true true true
}
