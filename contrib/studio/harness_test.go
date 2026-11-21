package studio

import (
	"bytes"
	"log/slog"
	"net/http"
	"testing"
)

// harness is a Studio over a fake MongoDB and a recorded Redis.
type harness struct {
	s     *Studio
	mongo *MongoDB
	fm    *fakeMongo
	redis *RedisDB
	rec   *recorder
	logs  *bytes.Buffer
	login *http.Cookie
}

func newHarness(t *testing.T, write bool) *harness {
	t.Helper()

	m, fm := newMongo(t, write, rawDoc(t, int32(1), "ada"), rawDoc(t, int32(2), "linus"))
	rdb, rec := fakeRedis(t, "string")

	h := &harness{mongo: m, fm: fm, redis: rdb, rec: rec, logs: &bytes.Buffer{}}

	s, err := New(Config{
		Sources: []Source{m, rdb}, Version: "v0.0.0-test", Write: write,
		Log: slog.New(slog.NewTextHandler(h.logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	h.s, h.login = s, session(t, s)

	return h
}
