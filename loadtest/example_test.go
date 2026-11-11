package loadtest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/loadtest"
)

func ExampleRun() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	sum, err := loadtest.Run(context.Background(), loadtest.Config{Concurrency: 4, Requests: 20},
		loadtest.Static(loadtest.Request{URL: srv.URL + "/"}))
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(sum.HTTPReqs, sum.HTTPReqFailed)
	// Output: 20 0
}
