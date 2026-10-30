package mail

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/view"
)

var update = flag.Bool("update", false, "rewrite golden files")

// testdata/react/welcome.html is real `email export` output (react-email
// 6.11.6, @react-email/components 1.0.12) of the component in
// docs/subsystems.md "React Email": Go template actions as default props.
func reactViews(t *testing.T) *view.Engine {
	t.Helper()

	e, err := view.New(view.Config{FS: os.DirFS("testdata/react")})
	if err != nil {
		t.Fatal(err)
	}

	return e
}

type reactWelcome struct {
	Name, URL string
}

func (reactWelcome) Envelope() Envelope { return Envelope{To: []string{"ana@example.com"}} }
func (m reactWelcome) Content() Content { return React("welcome", m) }

func golden(t *testing.T, path, got string) {
	t.Helper()

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s differs (go test -run %s -update):\n%s", path, t.Name(), got)
	}
}

func TestReactGolden(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "app@example.com", nil)
	m.SetReact(reactViews(t))

	msg, err := m.Render(t.Context(), reactWelcome{Name: "Ana", URL: "https://example.com/start"})
	if err != nil {
		t.Fatal(err)
	}

	golden(t, "testdata/react/welcome.golden.html", msg.HTML)
	golden(t, "testdata/react/welcome.golden.txt", msg.Text)

	if strings.Contains(msg.Text, "mail.welcome.preview") {
		t.Errorf("preheader leaked into the text part: %q", msg.Text)
	}
}

func TestSecurity_A03_ReactPropsEscaped(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "app@example.com", nil)
	m.SetReact(reactViews(t))

	msg, err := m.Render(t.Context(), reactWelcome{
		Name: `<script>alert(1)</script>"'`,
		URL:  "javascript:alert(1)",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"<script>", "javascript:"} {
		if strings.Contains(msg.HTML, bad) {
			t.Errorf("HTML contains %q", bad)
		}
	}

	if !strings.Contains(msg.HTML, "&lt;script&gt;") || !strings.Contains(msg.HTML, `href="#ZgotmplZ"`) {
		t.Errorf("props not escaped by context:\n%s", msg.HTML)
	}
}

func TestReactErrors(t *testing.T) {
	t.Parallel()

	if _, err := New(NewArray(), "app@example.com", nil).Render(t.Context(), reactWelcome{}); err == nil ||
		!strings.Contains(err.Error(), "WithReactEmails") {
		t.Errorf("no templates: err = %v", err)
	}

	m := New(NewArray(), "app@example.com", nil)
	m.SetReact(reactViews(t))

	_, err := m.Render(t.Context(), raw{
		env:     Envelope{To: []string{"ana@example.com"}},
		content: React("missing", nil),
	})
	if err == nil || !strings.Contains(err.Error(), `"missing"`) {
		t.Errorf("missing template: err = %v", err)
	}
}
