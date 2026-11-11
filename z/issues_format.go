package z

import (
	"strconv"
	"strings"
)

// FlatIssues is the result of [Issues.Flatten].
type FlatIssues struct {
	// Form holds the messages about the root value.
	Form []string `json:"form"`
	// Fields maps each path to its messages.
	Fields map[string][]string `json:"fields"`
}

// IssueNode is one level of the tree [Issues.Tree] returns.
type IssueNode struct {
	// Errors holds the messages of this exact value.
	Errors []string `json:"errors,omitempty"`
	// Properties holds the issues of object keys.
	Properties map[string]*IssueNode `json:"properties,omitempty"`
	// Items holds the issues of list positions.
	Items map[int]*IssueNode `json:"items,omitempty"`
}

// Flatten groups the messages by path: the ones about the root value
// (empty path) go to Form, the others to Fields under their dotted path.
// Both are never nil.
func (is Issues) Flatten() FlatIssues {
	flat := FlatIssues{Form: []string{}, Fields: map[string][]string{}}

	for _, i := range is {
		if i.Path == "" {
			flat.Form = append(flat.Form, i.Message)
			continue
		}

		flat.Fields[i.Path] = append(flat.Fields[i.Path], i.Message)
	}

	return flat
}

// Tree nests the messages by path: "author.email" is
// Properties["author"].Properties["email"] and "tags.2" is
// Properties["tags"].Items[2]. The root is never nil.
func (is Issues) Tree() *IssueNode {
	root := &IssueNode{}

	for _, i := range is {
		node := root

		if i.Path != "" {
			for seg := range strings.SplitSeq(i.Path, ".") {
				node = node.child(seg)
			}
		}

		node.Errors = append(node.Errors, i.Message)
	}

	return root
}

// child returns the node under seg, creating it: a number is a list position.
func (n *IssueNode) child(seg string) *IssueNode {
	if isIndex(seg) {
		if idx, err := strconv.Atoi(seg); err == nil {
			if n.Items == nil {
				n.Items = map[int]*IssueNode{}
			}

			if n.Items[idx] == nil {
				n.Items[idx] = &IssueNode{}
			}

			return n.Items[idx]
		}
	}

	if n.Properties == nil {
		n.Properties = map[string]*IssueNode{}
	}

	if n.Properties[seg] == nil {
		n.Properties[seg] = &IssueNode{}
	}

	return n.Properties[seg]
}

// Pretty renders the issues as text for a log or a terminal: one block per
// issue, in the order they came, with the path when there is one.
//
//	✖ The title field must be at least 3 characters.
//	  → at title
//	✖ The value must be valid JSON.
func (is Issues) Pretty() string {
	var b strings.Builder

	for n, i := range is {
		if n > 0 {
			b.WriteByte('\n')
		}

		b.WriteString("✖ " + i.Message)

		if i.Path != "" {
			b.WriteString("\n  → at " + i.Path)
		}
	}

	return b.String()
}
