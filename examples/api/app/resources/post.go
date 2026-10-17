// Package resources is the JSON wire format of the models.
package resources

import (
	"time"

	"github.com/AlexGalhardo/vukem/examples/api/app/models"
)

// PostJSON is the wire format of models.Post.
type PostJSON struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Post transforms one model.
func Post(p models.Post) PostJSON {
	return PostJSON{ID: p.ID, Title: p.Title, Body: p.Body, CreatedAt: p.CreatedAt}
}
