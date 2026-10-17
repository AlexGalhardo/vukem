// Package requests holds validated request inputs.
package requests

import "github.com/AlexGalhardo/vukem/z"

// Project is the input of the create and edit forms.
type Project struct {
	Name        string `json:"name"        form:"name"`
	Description string `json:"description" form:"description"`
}

// ProjectSchema validates Project.
var ProjectSchema = z.Struct[Project](z.Shape{
	"name":        z.String().Trim().Min(3).Max(80),
	"description": z.String().Trim().Max(2_000).Optional(),
})
