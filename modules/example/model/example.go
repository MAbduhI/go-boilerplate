// Package model holds the example domain model.
package model

import (
	base "github.com/MAbduhI/go-boilerplate/shared/database/model"
)

// Example is the example domain model.
//
// The embedded base carries the primary key and audit timestamps. Struct tags
// name GORM columns, but no GORM type appears here, so the model stays a plain
// data structure that a usecase can build and test without a database.
type Example struct {
	base.BaseModel

	// TODO: replace these placeholder fields with the real ones.
	Name        string `json:"name" gorm:"type:varchar(255);not null;index"`
	Description string `json:"description" gorm:"type:text"`
}

// TableName pins the table name so a rename of the Go type does not silently
// repoint the model at a different table.
func (Example) TableName() string { return "examples" }
