package htmx

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(t *testing.T, headers map[string]string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	for k, v := range headers {
		r.Header.Set(k, v)
	}

	return r
}

func TestRequestHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                        string
		h                           map[string]string
		is, partial, boost, restore bool
	}{
		{name: "browser", h: nil},
		{
			name:    "targeted swap",
			h:       map[string]string{HeaderRequest: "true", HeaderRequestType: "partial"},
			is:      true,
			partial: true,
		},
		{name: "no type header (htmx 2)", h: map[string]string{HeaderRequest: "true"}, is: true, partial: true},
		{name: "full", h: map[string]string{HeaderRequest: "true", HeaderRequestType: "full"}, is: true},
		{
			name:  "boosted",
			h:     map[string]string{HeaderRequest: "true", HeaderRequestType: "full", HeaderBoosted: "true"},
			is:    true,
			boost: true,
		},
		{
			name:    "history restore",
			h:       map[string]string{HeaderRequest: "true", HeaderHistoryRestore: "true"},
			is:      true,
			restore: true,
		},
	}

	for _, tt := range tests {
		r := req(t, tt.h)
		if Is(r) != tt.is || IsPartial(r) != tt.partial || IsBoosted(r) != tt.boost ||
			IsHistoryRestore(r) != tt.restore {
			t.Errorf(
				"%s: Is=%v Partial=%v Boosted=%v Restore=%v",
				tt.name,
				Is(r),
				IsPartial(r),
				IsBoosted(r),
				IsHistoryRestore(r),
			)
		}
	}

	r := req(
		t,
		map[string]string{HeaderSource: "button#save", HeaderTarget: "div#list", HeaderCurrentURL: "http://x/posts"},
	)
	if Source(r) != (Element{"button", "save"}) || Target(r) != (Element{"div", "list"}) ||
		CurrentURL(r) != "http://x/posts" {
		t.Fatalf("Source=%v Target=%v", Source(r), Target(r))
	}
	if Target(req(t, nil)) != (Element{}) {
		t.Fatal("empty target")
	}
}

func TestResponseHeaders(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewResponse(rec).
		Trigger("saved", map[string]int{"id": 1}).
		Trigger("refresh-list", nil).
		PushURL("/posts/1").
		ReplaceURL("/posts/1?x").
		Retarget("#flash").
		Reswap("outerMorph").
		Reselect("#main").
		Location("/next").
		Redirect("/full").
		Refresh()

	want := map[string]string{
		HeaderTrigger:    `{"refresh-list":null,"saved":{"id":1}}`,
		HeaderPushURL:    "/posts/1",
		HeaderReplaceURL: "/posts/1?x",
		HeaderRetarget:   "#flash",
		HeaderReswap:     "outerMorph",
		HeaderReselect:   "#main",
		HeaderLocation:   "/next",
		HeaderRedirect:   "/full",
		HeaderRefresh:    "true",
		"Vary":           HeaderRequest,
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	// Vary is added once.
	NewResponse(rec)
	if len(rec.Header().Values("Vary")) != 1 {
		t.Fatalf("Vary = %v", rec.Header().Values("Vary"))
	}

	rec = httptest.NewRecorder()
	NewResponse(rec).LocationWith(LocationOptions{Path: "/p", Target: "#main", Swap: "innerHTML"})
	if got := rec.Header().Get(HeaderLocation); got != `{"path":"/p","target":"#main","swap":"innerHTML"}` {
		t.Fatalf("LocationWith = %s", got)
	}
}

func TestSecurity_A05_FragmentsEscapeAttributes(t *testing.T) {
	t.Parallel()

	got := Partial(`#a" onmouseover="x`, "beforeend", template.HTML("<li>x</li>"))
	want := template.HTML(
		`<hx-partial hx-target="#a&#34; onmouseover=&#34;x" hx-swap="beforeend"><li>x</li></hx-partial>`,
	)
	if got != want {
		t.Fatalf("Partial = %s", got)
	}

	if got := Partial("#m", "", "ok"); got != `<hx-partial hx-target="#m">ok</hx-partial>` {
		t.Fatalf("Partial no swap = %s", got)
	}

	if got := OOB("count", "", "3"); got != `<div id="count" hx-swap-oob="true">3</div>` {
		t.Fatalf("OOB = %s", got)
	}
}
