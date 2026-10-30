package qrcode

import (
	"errors"
	"net/http"
	"strconv"
)

// Handler serves a PNG QR code. GET and HEAD read the payload from the
// data query parameter and write image/png. A missing or empty data
// value is 400. Any other method is 405. A payload that fails validation
// or cannot be encoded is 400 and the value is not echoed. Invalid
// [Options] are 500: they are the caller's configuration, not the request.
func Handler(opt Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		data := r.URL.Query().Get("data")
		if data == "" {
			http.Error(w, "missing data", http.StatusBadRequest)

			return
		}

		body, err := PNG(data, opt)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrInvalidOptions) {
				status = http.StatusInternalServerError
			}

			http.Error(w, http.StatusText(status), status)

			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)

		if r.Method == http.MethodHead {
			return
		}

		// The bytes are a PNG from the encoder, not HTML from the query string.
		if _, err := w.Write(body); err != nil { //nolint:gosec // G705: image/png body, not markup
			return
		}
	})
}
