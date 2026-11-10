package tests

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/vukemtest"
)

var linkRe = regexp.MustCompile(`https?://[^\s)"]+`)

// link extracts the first URL from the last mail sent to email.
func link(t *testing.T, app *vukemtest.App, email string) string {
	t.Helper()

	sent := app.Mails().SentTo(email)
	if len(sent) == 0 {
		t.Fatalf("no mail sent to %s", email)
	}

	u := linkRe.FindString(sent[len(sent)-1].Text)
	if u == "" {
		t.Fatalf("no link in mail: %q", sent[len(sent)-1].Text)
	}

	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}

	return parsed.RequestURI()
}

func register(t *testing.T, app *vukemtest.App, c *vukemtest.Client, email string) {
	t.Helper()

	c.PostForm("/register", url.Values{
		"name": {"Ana Silva"}, "email": {email},
		"password": {"correct horse battery"}, "password_confirmation": {"correct horse battery"},
	}).AssertRedirect("/register/check-email")
}

func TestRegisterAndVerifyEmail(t *testing.T) {
	app := newApp(t)
	c := app.Client(t)

	c.Get("/dashboard").AssertRedirect("/login")

	register(t, app, c, "ana@example.com")
	vukemtest.AssertDatabaseHas(t, app, "users", map[string]any{"email": "ana@example.com", "email_verified_at": nil})

	// Sign-up does not log in: the user proves the address first.
	c.Get("/dashboard").AssertRedirect("/login")
	c.Get("/register/check-email").AssertOK().AssertSee("Check your email")

	c.PostForm("/login", url.Values{"email": {"ana@example.com"}, "password": {"correct horse battery"}}).
		AssertRedirect("/dashboard")
	c.Get("/email/verify").AssertOK().AssertSee("verify")

	// Unverified accounts can sign in but not write.
	c.Get("/dashboard").AssertRedirect("/email/verify")
	c.Get("/posts/create").AssertRedirect("/email/verify")
	c.PostForm("/posts", url.Values{"title": {"Too early"}, "body": {"x"}}).AssertRedirect("/email/verify")
	c.PostForm("/posts/1/comments", url.Values{"body": {"Too early"}}).AssertRedirect("/email/verify")

	verify := link(t, app, "ana@example.com")
	app.Client(t).Get(verify).AssertRedirect("/login") // the link needs the account's session
	c.Get(verify + "x").AssertForbidden()              // tampered signature
	c.Get(verify).AssertRedirect("/dashboard")
	c.Get("/dashboard").AssertOK().AssertSee("Ana Silva").AssertSee("verified")
	c.Get("/posts/create").AssertOK()
}

// An address that already has an account gets the same answer as a new
// one, and nothing about the existing account changes.
func TestRegisterDoesNotRevealExistingEmails(t *testing.T) {
	app := newApp(t)
	register(t, app, app.Client(t), "ana@example.com")

	c := app.Client(t)
	c.WithHeader("Referer", "/register").PostForm("/register", url.Values{
		"name": {"Other"}, "email": {"ANA@example.com"},
		"password": {"another long password"}, "password_confirmation": {"another long password"},
	}).AssertRedirect("/register/check-email").AssertSessionMissing("errors")

	vukemtest.AssertDatabaseCount(t, app, "users", 1)
	vukemtest.AssertDatabaseHas(t, app, "users", map[string]any{"email": "ana@example.com", "name": "Ana Silva"})
}

func TestRegisterIsThrottled(t *testing.T) {
	app := newApp(t)
	c := app.Client(t)

	for i := range 10 {
		register(t, app, c, "user"+strconv.Itoa(i)+"@example.com")
	}

	c.PostForm("/register", url.Values{
		"name": {"Ana Silva"}, "email": {"user10@example.com"},
		"password": {"correct horse battery"}, "password_confirmation": {"correct horse battery"},
	}).AssertStatus(429)

}

// One address is limited on its own, however few requests the IP sent.
func TestRegisterIsThrottledPerEmail(t *testing.T) {
	app := newApp(t)
	other := app.Client(t)

	for range 5 {
		other.PostForm("/register", url.Values{
			"name": {"Ana Silva"}, "email": {"same@example.com"},
			"password": {"correct horse battery"}, "password_confirmation": {"correct horse battery"},
		})
	}

	other.PostForm("/register", url.Values{
		"name": {"Ana Silva"}, "email": {"same@example.com"},
		"password": {"correct horse battery"}, "password_confirmation": {"correct horse battery"},
	}).AssertStatus(429)
}

func TestLoginLogoutAndThrottle(t *testing.T) {
	app := newApp(t)
	register(t, app, app.Client(t), "bob@example.com")

	c := app.Client(t)
	c.WithHeader("Referer", "/login").
		PostForm("/login", url.Values{"email": {"bob@example.com"}, "password": {"wrong"}}).
		AssertSessionHasErrors("email")

	c.PostForm("/login", url.Values{"email": {"bob@example.com"}, "password": {"correct horse battery"}}).
		AssertRedirect("/dashboard")
	c.Get("/login").AssertRedirect("/")
	c.PostForm("/logout", nil).AssertRedirect("/")
	c.Get("/dashboard").AssertRedirect("/login")

	attacker := app.Client(t)
	for range 4 {
		attacker.PostForm("/login", url.Values{"email": {"bob@example.com"}, "password": {"guess"}})
	}

	attacker.PostForm("/login", url.Values{"email": {"bob@example.com"}, "password": {"guess"}}).AssertStatus(429)
}

func TestPasswordReset(t *testing.T) {
	app := newApp(t)
	register(t, app, app.Client(t), "cid@example.com")

	c := app.Client(t)

	// Unknown emails get the same answer and no mail.
	c.PostForm("/forgot-password", url.Values{"email": {"nobody@example.com"}}).AssertRedirect("/forgot-password")

	if len(app.Mails().SentTo("nobody@example.com")) != 0 {
		t.Fatal("mail sent to an unknown address")
	}

	c.PostForm("/forgot-password", url.Values{"email": {"cid@example.com"}}).AssertRedirect("/forgot-password")

	reset := link(t, app, "cid@example.com")
	if !strings.HasPrefix(reset, "/reset-password/") {
		t.Fatalf("reset link = %s", reset)
	}

	page := c.Get(reset).AssertOK()
	u, _ := url.Parse(reset)
	token := strings.TrimPrefix(u.Path, "/reset-password/")

	page.AssertSee(token)

	form := url.Values{
		"token": {token}, "email": {"cid@example.com"},
		"password": {"a brand new password"}, "password_confirmation": {"a brand new password"},
	}

	c.PostForm("/reset-password", form).AssertRedirect("/login")

	// Single use.
	c.WithHeader("Referer", reset).PostForm("/reset-password", form).AssertSessionHasErrors("email")

	c.PostForm("/login", url.Values{"email": {"cid@example.com"}, "password": {"a brand new password"}}).
		AssertRedirect("/dashboard")
}
