package model

import (
	"time"

	"github.com/google/uuid"
)

// BaseModelUUID is the alternative base for tables keyed by UUID. Select it per
// entity with `--base BaseModelUUID` on the generator, or repo-wide by setting
// shared.base_model.type in .forge.yaml.
type BaseModelUUID struct {
	ID        uuid.UUID  `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	CreatedAt time.Time  `json:"created_at" gorm:"type:timestamptz;not null;autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"type:timestamptz;not null;autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"type:timestamptz;index"`
}

// EnsureID assigns a UUIDv7 when the key is still zero, so the application can
// know a record's identity before it is written. v7 is time-ordered, which
// keeps B-tree index inserts local instead of scattering them the way v4 does.
func (b *BaseModelUUID) EnsureID() error {
	if b.ID != uuid.Nil {
		return nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	b.ID = id
	return nil
}

// IsDeleted reports whether the row is soft deleted.
func (b BaseModelUUID) IsDeleted() bool { return b.DeletedAt != nil }

// MarkDeleted stamps the soft-delete timestamp.
func (b *BaseModelUUID) MarkDeleted(at time.Time) {
	t := at.UTC()
	b.DeletedAt = &t
}

// Restore clears the soft-delete timestamp.
func (b *BaseModelUUID) Restore() { b.DeletedAt = nil }
