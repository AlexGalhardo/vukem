package mcp

import (
	"encoding/base64"
	"slices"
	"sort"
)

// pageResult sorts keys, applies the cursor of the request and returns one
// page under field. The cursor is the last key of the previous page, so it
// stays valid when items are added or removed between requests.
func pageResult(s *Server, c *call, field string, keys []string, item func(string) any) (any, *Error) {
	var p struct {
		Cursor *string `json:"cursor"`
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return nil, perr
	}

	sort.Strings(keys)

	if p.Cursor != nil {
		raw, err := base64.RawURLEncoding.DecodeString(*p.Cursor)
		if err != nil {
			return nil, invalidParams("invalid cursor")
		}

		after := string(raw)
		keys = keys[sort.Search(len(keys), func(i int) bool { return keys[i] > after }):]
	}

	next := ""

	if len(keys) > s.cfg.PageSize {
		keys = keys[:s.cfg.PageSize]
		next = base64.RawURLEncoding.EncodeToString([]byte(keys[len(keys)-1]))
	}

	items := make([]any, 0, len(keys))
	for _, k := range slices.Clone(keys) {
		items = append(items, item(k))
	}

	out := map[string]any{field: items, "ttlMs": s.cfg.CacheTTL.Milliseconds(), "cacheScope": "private"}
	if next != "" {
		out["nextCursor"] = next
	}

	return out, nil
}
