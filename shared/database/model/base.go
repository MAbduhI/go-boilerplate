// Package model holds the base structs that feature models embed.
//
// Nothing here imports GORM. The struct tags name GORM's column options, which
// costs nothing at compile time, but the types stay standard library so a
// domain model never depends on the persistence layer. That is what makes
// gorm.DeletedAt absent: it is a GORM type, so soft delete is expressed as a
// nullable timestamp and applied explicitly by the repository via
// postgres.NotDeleted.
package model

import "time"

// BaseModel is the default embeddable base: an auto-increment integer key plus
// audit timestamps.
type BaseModel struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"type:timestamptz;not null;autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"type:timestamptz;not null;autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"type:timestamptz;index"`
}

// IsDeleted reports whether the row is soft deleted.
func (b BaseModel) IsDeleted() bool { return b.DeletedAt != nil }

// MarkDeleted stamps the soft-delete timestamp.
func (b *BaseModel) MarkDeleted(at time.Time) {
	t := at.UTC()
	b.DeletedAt = &t
}

// Restore clears the soft-delete timestamp.
func (b *BaseModel) Restore() { b.DeletedAt = nil }
