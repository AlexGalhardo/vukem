// Package models holds the database models.
package models

import "github.com/AlexGalhardo/vukem/orm"

// Project is a row of the projects table, owned by one user.
type Project struct {
	orm.Model

	UserID      int64  `json:"-"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
