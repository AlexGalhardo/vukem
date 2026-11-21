package studio

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// routes registers every route; nothing is registered outside this method, so
// every route is behind Handler's guards. The interface is a React build
// served under /ui/; the JSON API under /api/v1/ is what it talks to. Without
// --write the write routes do not exist: they answer 404.
func (s *Studio) routes() {
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusSeeOther)
	})
	s.uiRoutes()
	s.apiRoutes()
}

const (
	minRefresh     = 500 * time.Millisecond
	defaultRefresh = 2 * time.Second
)

// refreshInterval normalizes Config.Refresh: 0 means the default (a negative
// value turns live updates off), and anything shorter than 500 ms is raised.
func refreshInterval(d time.Duration) time.Duration {
	switch {
	case d < 0:
		return 0
	case d == 0:
		return defaultRefresh
	default:
		return max(d, minRefresh)
	}
}

func (s *Studio) source(r *http.Request) Source {
	for _, src := range s.cfg.Sources {
		if src.Name() == r.PathValue("source") {
			return src
		}
	}

	return nil
}

func (s *Studio) sql(r *http.Request) SQLSource {
	src, _ := s.source(r).(SQLSource)
	return src
}

func (s *Studio) mongo(r *http.Request) MongoSource {
	m, _ := s.source(r).(MongoSource)
	return m
}

func (s *Studio) redis(r *http.Request) RedisSource {
	m, _ := s.source(r).(RedisSource)
	return m
}

// errInput marks a refusal caused by what the browser sent.
var errInput = errors.New("invalid input")

func inputError(msg string) error { return errors.Join(errInput, errors.New(msg)) }

// confirmText is what a person types to confirm a change to a document: the
// hex of an ObjectID, the digits of an integer _id, the text of a string _id,
// otherwise the canonical Extended JSON of the _id.
func confirmText(id string) string {
	if h, ok := strings.CutPrefix(id, `{"$oid":"`); ok && strings.HasSuffix(h, `"}`) {
		return strings.TrimSuffix(h, `"}`)
	}

	for _, wrap := range []string{`{"$numberInt":"`, `{"$numberLong":"`} {
		if n, ok := strings.CutPrefix(id, wrap); ok && strings.HasSuffix(n, `"}`) {
			return strings.TrimSuffix(n, `"}`)
		}
	}

	if len(id) >= 2 && id[0] == '"' && id[len(id)-1] == '"' {
		if s, err := strconv.Unquote(id); err == nil {
			return s
		}
	}

	return id
}

// encodeCursor packs a keyset cursor into one query value; the first page has
// no cursor and packs to "".
func encodeCursor(key []string) string {
	parts := make([]string, len(key))
	for i, k := range key {
		parts[i] = url.QueryEscape(k)
	}

	return strings.Join(parts, ",")
}

func decodeCursor(s string) []string {
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i], _ = url.QueryUnescape(p)
	}

	return parts
}
