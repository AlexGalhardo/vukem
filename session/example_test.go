package session_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/web"
)

func Example() {
	m, err := session.New(session.Config{
		Store: session.NewMemoryStore(), // nil = encrypted cookie sessions (CookieKey)
		Log:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	visits := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, _ := web.SessionFrom(r.Context()).(*session.Session)
		n, _ := s.GetInt64("visits")
		s.Put("visits", n+1)
		fmt.Fprintf(w, "visit %d", n+1)
	}))

	var cookies []*http.Cookie
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		rec := httptest.NewRecorder()
		visits.ServeHTTP(rec, req)
		cookies = rec.Result().Cookies()
		fmt.Println(rec.Body.String())
	}
	// Output:
	// visit 1
	// visit 2
}
