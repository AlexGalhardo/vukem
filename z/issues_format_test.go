package z

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func formatIssues() Issues {
	return Issues{
		{Path: "", Code: CodeInvalidType, Message: "The value must be valid JSON."},
		{Path: "title", Code: CodeTooSmall, Message: "The title field must be at least 3 characters."},
		{Path: "author.email", Code: CodeInvalidString, Message: "The email field must be a valid email address."},
		{Path: "tags.2", Code: CodeTooSmall, Message: "first"},
		{Path: "tags.2", Code: CodeInvalidString, Message: "second"},
	}
}

func TestIssuesFlatten(t *testing.T) {
	t.Parallel()

	want := FlatIssues{
		Form: []string{"The value must be valid JSON."},
		Fields: map[string][]string{
			"title":        {"The title field must be at least 3 characters."},
			"author.email": {"The email field must be a valid email address."},
			"tags.2":       {"first", "second"},
		},
	}
	if diff := cmp.Diff(want, formatIssues().Flatten()); diff != "" {
		t.Errorf("Flatten (-want +got):\n%s", diff)
	}

	empty := Issues(nil).Flatten()
	if empty.Form == nil || empty.Fields == nil || len(empty.Form) != 0 || len(empty.Fields) != 0 {
		t.Errorf("Flatten(nil) = %+v, want empty non-nil members", empty)
	}
}

func TestIssuesTree(t *testing.T) {
	t.Parallel()

	want := &IssueNode{
		Errors: []string{"The value must be valid JSON."},
		Properties: map[string]*IssueNode{
			"title": {Errors: []string{"The title field must be at least 3 characters."}},
			"author": {Properties: map[string]*IssueNode{
				"email": {Errors: []string{"The email field must be a valid email address."}},
			}},
			"tags": {Items: map[int]*IssueNode{2: {Errors: []string{"first", "second"}}}},
		},
	}
	if diff := cmp.Diff(want, formatIssues().Tree()); diff != "" {
		t.Errorf("Tree (-want +got):\n%s", diff)
	}

	if root := Issues(nil).Tree(); root == nil || len(root.Errors) != 0 || root.Properties != nil || root.Items != nil {
		t.Errorf("Tree(nil) = %+v, want an empty root", root)
	}
}

func TestIssuesPretty(t *testing.T) {
	t.Parallel()

	want := "✖ The value must be valid JSON.\n" +
		"✖ The title field must be at least 3 characters.\n  → at title\n" +
		"✖ The email field must be a valid email address.\n  → at author.email\n" +
		"✖ first\n  → at tags.2\n" +
		"✖ second\n  → at tags.2"
	if got := formatIssues().Pretty(); got != want {
		t.Errorf("Pretty = %q, want %q", got, want)
	}

	if got := Issues(nil).Pretty(); got != "" {
		t.Errorf("Pretty(nil) = %q, want empty", got)
	}
}
