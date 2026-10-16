package requests

import "github.com/AlexGalhardo/golang-framework/z"

// StorePost is the validated input; declare fields with json/form and validate tags.
type StorePost struct{}

// StorePostSchema validates StorePost; use it with web.Parse(c, StorePostSchema).
var StorePostSchema = z.For[StorePost]()
