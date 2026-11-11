package liveui

import (
	"regexp"
	"strings"
	"testing"
)

func TestTemplatesParse(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"templates/live.html", "templates/report.html"} {
		b, err := assetFS.ReadFile(name)
		if err != nil || len(b) == 0 {
			t.Fatalf("%s: %v (%d bytes)", name, err, len(b))
		}
	}

	if _, err := parseReportTemplate(); err != nil {
		t.Fatal(err)
	}
}

func TestAssetsEmbedded(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"assets/live.css", "assets/live.js", "templates/live.html", "templates/report.html"} {
		if b, err := assetFS.ReadFile(name); err != nil || len(b) < 100 {
			t.Errorf("%s is missing or empty: %v", name, err)
		}
	}
}

func TestSecurity_A05_BenchUIHasNoInlineCode(t *testing.T) {
	t.Parallel()

	b, err := assetFS.ReadFile("templates/live.html")
	if err != nil {
		t.Fatal(err)
	}

	page := string(b)

	for _, m := range regexp.MustCompile(`(?is)<script\b[^>]*>`).FindAllString(page, -1) {
		if !strings.Contains(m, "src=") {
			t.Errorf("an inline <script> in the live page: %s", m)
		}
	}

	if strings.Contains(strings.ToLower(page), "<style") || strings.Contains(strings.ToLower(page), `style="`) {
		t.Error("an inline style in the live page")
	}

	if m := regexp.MustCompile(`(?i)\son[a-z]+\s*=`).FindString(page); m != "" {
		t.Errorf("an inline event handler in the live page: %q", m)
	}

	if strings.Contains(page, "javascript:") || strings.Contains(page, "http://") ||
		strings.Contains(page, "https://") {
		t.Error("the live page refers to an external or scripted URL")
	}

	css, _ := assetFS.ReadFile("assets/live.css")
	if strings.Contains(string(css), "url(") || strings.Contains(string(css), "@import") {
		t.Error("the stylesheet loads something: it must stay self-contained (the report inlines it)")
	}
}

func TestSecurity_A03_BenchUIScriptNeverUsesInnerHTML(t *testing.T) {
	t.Parallel()

	b, err := assetFS.ReadFile("assets/live.js")
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "setTimeout(\"", "importScripts"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("live.js contains %q: scenario names are user input, so the script may only use textContent", bad)
		}
	}
}
