// Package repository is the persistence layer for example.
//
// It lives in this module's own go.mod, so no other module can import it: a
// feature's data access is reachable only through its usecase.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/MAbduhI/go-boilerplate/shared/database/postgres"
	"github.com/MAbduhI/go-boilerplate/shared/httpx"

	"github.com/MAbduhI/go-boilerplate/modules/example/model"
)

// ListParams narrows a list query.
type ListParams struct {
	Offset int
	Limit  int
	Search string
}

// ExampleRepository is the persistence contract the usecase depends on.
// The usecase takes this interface, not the struct, so it can be tested against
// a fake.
type ExampleRepository interface {
	List(ctx context.Context, params ListParams) ([]model.Example, int64, error)
	GetByID(ctx context.Context, id uint) (*model.Example, error)
	Create(ctx context.Context, m *model.Example) error
	Update(ctx context.Context, m *model.Example) error
	Delete(ctx context.Context, id uint) error
}

// exampleRepository is the GORM implementation. It is unexported: callers
// receive it as a ExampleRepository from NewExampleRepository.
type exampleRepository struct {
	db postgres.DB
}

// compile-time check that the implementation satisfies the interface.
var _ ExampleRepository = (*exampleRepository)(nil)

// NewExampleRepository returns a example repository backed by Postgres.
func NewExampleRepository(db postgres.DB) ExampleRepository {
	return &exampleRepository{db: db}
}

// List returns one page of examples plus the total matching count.
func (r *exampleRepository) List(ctx context.Context, params ListParams) ([]model.Example, int64, error) {
	var (
		out   []model.Example
		total int64
	)

	// Db(ctx) returns the ambient transaction when the caller opened one, so this
	// query joins it instead of running on a separate connection.
	q := r.db.Db(ctx).Model(&model.Example{}).Scopes(postgres.NotDeleted)

	if params.Search != "" {
		q = q.Where("name ILIKE ?", "%"+params.Search+"%")
	}

	// Count before applying limit/offset, or the total reflects only the page.
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count examples: %w", err)
	}
	if total == 0 {
		return []model.Example{}, 0, nil
	}

	if params.Limit > 0 {
		q = q.Limit(params.Limit).Offset(params.Offset)
	}
	if err := q.Order("id DESC").Find(&out).Error; err != nil {
		return nil, 0, fmt.Errorf("list examples: %w", err)
	}
	return out, total, nil
}

// GetByID returns one example, or a wrapped httpx.ErrNotFound.
func (r *exampleRepository) GetByID(ctx context.Context, id uint) (*model.Example, error) {
	var out model.Example

	err := r.db.Db(ctx).
		Scopes(postgres.NotDeleted).
		First(&out, "id = ?", id).Error
	if err != nil {
		// Translate the driver error into a domain error here, so neither the
		// usecase nor the handler needs to know about GORM.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, httpx.NotFound("example not found")
		}
		return nil, fmt.Errorf("get example %v: %w", id, err)
	}
	return &out, nil
}

// Create inserts a example and populates its generated key.
func (r *exampleRepository) Create(ctx context.Context, m *model.Example) error {
	if err := r.db.Db(ctx).Create(m).Error; err != nil {
		return fmt.Errorf("create example: %w", err)
	}
	return nil
}

// Update writes every column of an existing example.
func (r *exampleRepository) Update(ctx context.Context, m *model.Example) error {
	res := r.db.Db(ctx).
		Model(&model.Example{}).
		Where("id = ?", m.ID).
		Scopes(postgres.NotDeleted).
		Updates(m)
	if res.Error != nil {
		return fmt.Errorf("update example %v: %w", m.ID, res.Error)
	}
	if res.RowsAffected == 0 {
		return httpx.NotFound("example not found")
	}
	return nil
}

// Delete soft deletes a example by stamping deleted_at.
//
// The base model keeps deleted_at as a plain *time.Time rather than
// gorm.DeletedAt, which keeps the domain model free of GORM types but means
// GORM applies no automatic soft-delete filter. The stamp is therefore explicit
// here, and reads exclude deleted rows via the postgres.NotDeleted scope.
func (r *exampleRepository) Delete(ctx context.Context, id uint) error {
	res := r.db.Db(ctx).
		Model(&model.Example{}).
		Where("id = ?", id).
		Scopes(postgres.NotDeleted).
		Update("deleted_at", time.Now().UTC())
	if res.Error != nil {
		return fmt.Errorf("delete example %v: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return httpx.NotFound("example not found")
	}
	return nil
}
