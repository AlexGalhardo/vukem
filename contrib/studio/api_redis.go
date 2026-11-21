package studio

import (
	"net/http"
	"strconv"
	"time"
)

func (s *Studio) apiRedisRoutes() {
	s.mux.HandleFunc("GET /api/v1/sources/{source}/keys", s.apiKeys)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/keys/{key...}", s.apiKey)
	s.mux.HandleFunc("POST /api/v1/sources/{source}/command", s.apiCommand)
}

// apiRedis returns the Redis source of the path, answering 404 itself when
// the source does not exist or is not Redis.
func (s *Studio) apiRedis(w http.ResponseWriter, r *http.Request) RedisSource {
	src := s.redis(r)
	if src == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Not found.")
	}

	return src
}

// ttlSeconds is the time to live in whole seconds, -1 when the key does not
// expire.
func ttlSeconds(d time.Duration) int64 {
	if d <= 0 {
		return -1
	}

	return int64(d / time.Second)
}

type apiKeyInfo struct {
	Key        string `json:"key"`
	Type       string `json:"type"`
	TTLSeconds int64  `json:"ttlSeconds"`
}

func (s *Studio) apiKeys(w http.ResponseWriter, r *http.Request) {
	src := s.apiRedis(w, r)
	if src == nil {
		return
	}

	pg, err := src.Keys(r.Context(), keyQueryFrom(r.URL.Query()))
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	keys := make([]apiKeyInfo, len(pg.Keys))
	for i, k := range pg.Keys {
		keys[i] = apiKeyInfo{Key: k.Key, Type: k.Type, TTLSeconds: ttlSeconds(k.TTL)}
	}

	// The cursor is a string: a SCAN cursor can be larger than a JSON number
	// keeps exact in a browser.
	out := map[string]any{"keys": keys}
	if pg.Cursor != 0 {
		out["cursor"] = strconv.FormatUint(pg.Cursor, 10)
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Studio) apiKey(w http.ResponseWriter, r *http.Request) {
	src := s.apiRedis(w, r)
	if src == nil {
		return
	}

	d, err := src.Key(r.Context(), r.PathValue("key"), intParam(r.URL.Query(), "page"))
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	fields := d.Fields
	if fields == nil {
		fields = [][2]string{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key": d.Key, "type": d.Type, "ttlSeconds": ttlSeconds(d.TTL), "size": d.Size,
		"fields": fields, "more": d.More,
	})
}

type commandBody struct {
	Line string `json:"line"`
}

// apiCommand runs one allowed console command. Only its name is audited, never
// its arguments, which hold keys and values.
func (s *Studio) apiCommand(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiRedis(w, r)
	if src == nil {
		return
	}

	var body commandBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	out, err := src.Command(r.Context(), body.Line, s.cfg.Write)
	s.auditRedisCommand(r.Context(), src, body.Line, err)

	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": out})
}
