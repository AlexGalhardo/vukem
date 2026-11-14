package studio

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var hrefRe = regexp.MustCompile(`href="([^"]+)"`)

// hrefOf returns the unescaped href of the first link whose tag matches tag.
func hrefOf(t *testing.T, body, tag string) string {
	t.Helper()

	for _, m := range regexp.MustCompile(`<a [^>]*>`).FindAllString(body, -1) {
		if strings.Contains(m, tag) {
			if h := hrefRe.FindStringSubmatch(m); h != nil {
				return html.UnescapeString(h[1])
			}
		}
	}

	t.Fatalf("no link with %q in:\n%s", tag, body)

	return ""
}

func TestSQLGridHeadersSortAndToggle(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	_, body := get(s, c, "/s/db/t/items")
	if h := hrefOf(t, body, `data-sort="qty"`); !strings.Contains(h, "sort=qty") || strings.Contains(h, "desc=1") {
		t.Errorf("qty header link = %q, want an ascending sort", h)
	}

	_, body = get(s, c, "/s/db/t/items?sort=qty")
	if h := hrefOf(t, body, `data-sort="qty"`); !strings.Contains(h, "desc=1") {
		t.Errorf("sorted header link = %q, want the descending toggle", h)
	}

	if !strings.Contains(body, `aria-sort="ascending"`) {
		t.Error("the sorted column does not say aria-sort=ascending")
	}
}

func TestSQLGridPreviousPageOnKeyset(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, false)

	for _, q := range []string{
		`INSERT INTO items (id, qty) VALUES (2, 1)`, `INSERT INTO items (id, qty) VALUES (3, 1)`,
	} {
		if _, err := h.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	_, first := get(s, c, "/s/db/t/items?per=1")
	if strings.Contains(first, `rel="prev"`) {
		t.Error("the first page offers Previous")
	}

	_, second := get(s, c, hrefOf(t, first, `rel="next"`))
	if !strings.Contains(second, `rel="prev"`) {
		t.Fatalf("the second page has no Previous:\n%s", second)
	}

	_, third := get(s, c, hrefOf(t, second, `rel="next"`))

	_, back := get(s, c, hrefOf(t, third, `rel="prev"`))
	if !strings.Contains(back, ">2<") || strings.Contains(back, ">3<") {
		t.Errorf("Previous from the third page did not show the second row:\n%s", back)
	}

	_, back = get(s, c, hrefOf(t, back, `rel="prev"`))
	if !strings.Contains(back, "first") || strings.Contains(back, `rel="prev"`) {
		t.Errorf("Previous twice did not land on the first page:\n%s", back)
	}
}

func TestSQLGridExplainsFilterOperators(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	_, body := get(s, c, "/s/db/t/items")
	for _, op := range []string{"eq", "ne", "lt", "lte", "gt", "gte", "contains", "starts", "null", "notnull"} {
		if !strings.Contains(body, "<code>"+op+"</code>") {
			t.Errorf("the filter help does not list %q", op)
		}
	}
}

func TestSQLGridRefusesAMalformedFilter(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	code, body := get(s, c, "/s/db/t/items?f="+url.QueryEscape("qty"))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "column:op:value") {
		t.Errorf("a filter without an operator = %d, want 422 naming the format\n%s", code, body)
	}
}

func TestSQLRowPageLinksForeignKeysToTheReferencedRow(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	_, body := get(s, c, "/s/db/t/pets/r?k=1")
	if got := hrefOf(t, body, `data-fk="owners"`); got != "/s/db/t/owners/r?k=1" {
		t.Errorf("foreign key link = %q, want the owner row", got)
	}
}

func TestSQLWriteErrorKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, true)

	rec := postForm(s, c, "/s/db/t/items/r?k=1", url.Values{
		"action": {"update"}, "col.qty": {"not-a-number"}, "col.note": {"my long edit"},
	})

	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "my long edit") ||
		!strings.Contains(body, "not-a-number") {
		t.Errorf("failed update = %d, the page must repeat what was typed:\n%s", rec.Code, body)
	}

	if got := hrefOf(t, body, `data-back`); got != "/s/db/t/items/r?k=1" {
		t.Errorf("back link = %q, want the row page", got)
	}
}
