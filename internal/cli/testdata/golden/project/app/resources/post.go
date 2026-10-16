package resources

import (
	"time"

	"github.com/AlexGalhardo/vukem/api"

	"example.com/blog/app/models"
)

// PostJSON is the wire format of models.Post; add the fields
// clients may see.
type PostJSON struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Post transforms one model.
func Post(m models.Post) PostJSON {
	return PostJSON{ID: m.ID, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

// PostList transforms a slice of models.
func PostList(ms []models.Post) []PostJSON { return api.Map(ms, Post) }
