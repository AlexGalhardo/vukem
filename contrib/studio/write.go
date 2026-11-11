package studio

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// writeRoutes registers the routes that change data. Studio calls it only
// with Config.Write: otherwise they do not exist and answer 404.
func (s *Studio) writeRoutes() {
	s.mux.HandleFunc("POST /s/{source}/c/{collection}", s.insertDocument)
	s.mux.HandleFunc("POST /s/{source}/c/{collection}/d/{id}", s.changeDocument)
	s.mux.HandleFunc("POST /s/{source}/k/{key...}", s.changeKey)
	s.mux.HandleFunc("GET /s/{source}/t/{table}/new", s.newRowPage)
	s.mux.HandleFunc("POST /s/{source}/t/{table}", s.insertRow)
	s.mux.HandleFunc("POST /s/{source}/t/{table}/r", s.changeRow)
}

// audit logs one write attempt on stderr, before the response. Values never
// appear: only the source, what was done, to what, and whether it worked.
func (s *Studio) audit(ctx context.Context, src Source, action, target string, err error, attrs ...any) {
	args := append(
		[]any{"source", src.Name(), "kind", string(src.Kind()), "action", action, "target", target},
		attrs...)

	args = append(args, "ok", err == nil)
	if err != nil {
		args = append(args, "error", err.Error())
	}

	s.cfg.Log.WarnContext(ctx, "studio write", args...)
}

// auditConsole records a MongoDB aggregate that was allowed to write.
func (s *Studio) auditConsole(ctx context.Context, m MongoSource, coll string, err error) {
	if s.cfg.Write {
		s.audit(ctx, m, "aggregate", coll, err)
	}
}

// auditRedisCommand records a console command: only its name, never its
// arguments, which hold keys and values. Read commands are not writes.
func (s *Studio) auditRedisCommand(ctx context.Context, src RedisSource, line string, err error) {
	args, perr := parseRedisLine(line)
	if perr != nil || !s.cfg.Write || !isRedisWrite(args[0]) {
		return
	}

	s.audit(ctx, src, "console", strings.ToUpper(args[0]), err, "args", len(args)-1)
}

func isRedisWrite(cmd string) bool {
	return slices.Contains(redisWriteCommands, strings.ToUpper(cmd))
}

// form reads a bounded POST form.
func (s *Studio) form(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

	if err := r.ParseForm(); err != nil {
		return inputError("the form is invalid or too large")
	}

	return nil
}

// confirmed reports whether the typed confirmation equals what is being
// deleted.
func confirmed(r *http.Request, want string) bool {
	got := r.PostFormValue("confirm")

	return got != "" && got == want
}

func (s *Studio) insertDocument(w http.ResponseWriter, r *http.Request) {
	m := s.mongo(r)
	if m == nil {
		http.NotFound(w, r)
		return
	}

	if err := s.form(w, r); err != nil {
		s.fail(w, r, err)
		return
	}

	coll := r.PathValue("collection")
	err := m.Insert(r.Context(), coll, r.PostFormValue("doc"))
	s.audit(r.Context(), m, "insert", coll, err)

	if err != nil {
		s.fail(w, r, err)
		return
	}

	http.Redirect(w, r, "/s/"+url.PathEscape(m.Name())+"/c/"+url.PathEscape(coll), http.StatusSeeOther)
}

// changeDocument replaces or deletes one document, by _id. A delete needs the
// _id typed back: without it, or with another value, nothing runs (422).
func (s *Studio) changeDocument(w http.ResponseWriter, r *http.Request) {
	m := s.mongo(r)
	if m == nil {
		http.NotFound(w, r)
		return
	}

	if err := s.form(w, r); err != nil {
		s.fail(w, r, err)
		return
	}

	coll, id := r.PathValue("collection"), r.PathValue("id")
	back := "/s/" + url.PathEscape(m.Name()) + "/c/" + url.PathEscape(coll)

	var err error

	switch r.PostFormValue("action") {
	case "replace":
		err = m.Replace(r.Context(), coll, id, r.PostFormValue("doc"))
		s.audit(r.Context(), m, "replace", coll, err, "id", id)

		back += "/d/" + url.PathEscape(id)
	case "delete":
		if !confirmed(r, confirmText(id)) {
			s.audit(r.Context(), m, "delete", coll, inputError("confirmation did not match"), "id", id)
			s.fail(w, r, inputError("type the document's _id to confirm the deletion"))

			return
		}

		err = m.Delete(r.Context(), coll, id)
		s.audit(r.Context(), m, "delete", coll, err, "id", id)
	default:
		s.fail(w, r, inputError("unknown action"))
		return
	}

	if err != nil {
		s.fail(w, r, err)
		return
	}

	http.Redirect(w, r, back, http.StatusSeeOther)
}

// changeKey edits one Redis key: a string (keeping its TTL), a hash field, the
// TTL, or deletes it (after the key name is typed back).
func (s *Studio) changeKey(w http.ResponseWriter, r *http.Request) {
	src := s.redis(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	if err := s.form(w, r); err != nil {
		s.fail(w, r, err)
		return
	}

	key := r.PathValue("key")
	back := "/s/" + url.PathEscape(src.Name()) + "/k/" + keyPath(key)

	var err error

	switch action := r.PostFormValue("action"); action {
	case "set":
		err = src.SetString(r.Context(), key, r.PostFormValue("value"))
	case "hset":
		err = src.SetField(r.Context(), key, r.PostFormValue("field"), r.PostFormValue("value"))
	case "ttl":
		var secs int64

		if secs, err = strconv.ParseInt(strings.TrimSpace(r.PostFormValue("seconds")), 10, 64); err != nil {
			s.fail(w, r, inputError("seconds must be a whole number"))
			return
		}

		err = src.SetTTL(r.Context(), key, time.Duration(secs)*time.Second)
	case "delete":
		if !confirmed(r, key) {
			s.audit(r.Context(), src, "del", key, inputError("confirmation did not match"))
			s.fail(w, r, inputError("type the key name to confirm the deletion"))

			return
		}

		err = src.Delete(r.Context(), key)
		back = "/s/" + url.PathEscape(src.Name())
	default:
		s.fail(w, r, inputError("unknown action"))
		return
	}

	s.audit(r.Context(), src, r.PostFormValue("action"), key, err)

	if err != nil {
		s.fail(w, r, err)
		return
	}

	// back is built here from escaped path parts, never from a form value.
	http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710
}

// keyPath escapes each segment of a Redis key for a URL, keeping the slashes.
func keyPath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}

	return strings.Join(parts, "/")
}
