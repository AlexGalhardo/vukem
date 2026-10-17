package export_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"

	"github.com/AlexGalhardo/vukem/export"
)

func ExampleCSV() {
	users := seq(user{1, "Ana"}, user{2, "=SUM(A1:A9)"})

	handler := func(w http.ResponseWriter, r *http.Request) {
		// With the ORM: orm.QueryOf[User](db).Each(r.Context()) as rows.
		err := export.CSV(w, r, "users.csv", []string{"id", "name"}, users,
			func(u user) []string { return []string{strconv.Itoa(u.ID), u.Name} })
		// Before the first row nothing was sent and an error page still fits;
		// ErrIncomplete means the download already started and was aborted.
		if err != nil && !errors.Is(err, export.ErrIncomplete) {
			http.Error(w, "export failed", http.StatusInternalServerError)

			return
		}
	}

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/users.csv", http.NoBody))

	fmt.Println(rec.Header().Get("Content-Disposition"))
	fmt.Print(rec.Body.String())
	// Output:
	// attachment; filename="users.csv"
	// id,name
	// 1,Ana
	// 2,'=SUM(A1:A9)
}
