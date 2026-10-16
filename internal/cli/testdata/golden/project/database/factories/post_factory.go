package factories

import "example.com/blog/app/models"

// Post returns a models.Post with default values; overrides adjust it.
func Post(overrides ...func(*models.Post)) models.Post {
	var m models.Post
	for _, override := range overrides {
		override(&m)
	}

	return m
}
