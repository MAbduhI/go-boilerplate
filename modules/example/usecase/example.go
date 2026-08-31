// Package usecase holds the example business rules.
//
// It depends on the repository interface rather than on GORM, and on a local
// transaction interface rather than on the Postgres client, so it can be tested
// with fakes and knows nothing about the database.
package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/MAbduhI/go-boilerplate/shared/httpx"

	"github.com/MAbduhI/go-boilerplate/modules/example/model"
	"github.com/MAbduhI/go-boilerplate/modules/example/repository"
)

// txManager runs a function inside a database transaction.
//
// Declared here, in the consumer, rather than imported from the postgres
// package: the usecase needs exactly one method, and depending on the narrow
// interface keeps the layer free of persistence imports. *postgres.Client
// satisfies it structurally.
type txManager interface {
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// ListInput is a page request.
type ListInput struct {
	Page     int
	PageSize int
	Search   string
}

// ListOutput is a page of results plus the total count.
type ListOutput struct {
	Items []model.Example
	Total int64
}

// CreateInput describes a new example.
type CreateInput struct {
	Name        string
	Description string
}

// UpdateInput fully replaces a example.
type UpdateInput struct {
	ID          uint
	Name        string
	Description string
}

// PatchInput partially updates a example. A nil pointer means "leave this
// field alone", which is what distinguishes a partial update from a replacement.
type PatchInput struct {
	ID          uint
	Name        *string
	Description *string
}

// ExampleUsecase is the business contract the handler depends on.
type ExampleUsecase interface {
	List(ctx context.Context, in ListInput) (ListOutput, error)
	GetByID(ctx context.Context, id uint) (*model.Example, error)
	Create(ctx context.Context, in CreateInput) (*model.Example, error)
	Update(ctx context.Context, in UpdateInput) (*model.Example, error)
	Patch(ctx context.Context, in PatchInput) (*model.Example, error)
	Delete(ctx context.Context, id uint) error
}

// exampleUsecase is the implementation, unexported so callers hold the
// interface.
type exampleUsecase struct {
	repo repository.ExampleRepository
	tx   txManager
}

// compile-time check that the implementation satisfies the interface.
var _ ExampleUsecase = (*exampleUsecase)(nil)

// NewExampleUsecase returns the example usecase.
func NewExampleUsecase(repo repository.ExampleRepository, tx txManager) ExampleUsecase {
	return &exampleUsecase{repo: repo, tx: tx}
}

// List returns one page of examples.
func (u *exampleUsecase) List(ctx context.Context, in ListInput) (ListOutput, error) {
	if in.Page < 1 {
		in.Page = 1
	}
	if in.PageSize < 1 || in.PageSize > 200 {
		in.PageSize = 20
	}

	items, total, err := u.repo.List(ctx, repository.ListParams{
		Offset: (in.Page - 1) * in.PageSize,
		Limit:  in.PageSize,
		Search: strings.TrimSpace(in.Search),
	})
	if err != nil {
		return ListOutput{}, err
	}
	return ListOutput{Items: items, Total: total}, nil
}

// GetByID returns one example.
func (u *exampleUsecase) GetByID(ctx context.Context, id uint) (*model.Example, error) {
	return u.repo.GetByID(ctx, id)
}

// Create validates the input and inserts a example.
func (u *exampleUsecase) Create(ctx context.Context, in CreateInput) (*model.Example, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, httpx.BadRequest("name must not be empty")
	}

	m := &model.Example{
		Name:        name,
		Description: strings.TrimSpace(in.Description),
	}
	if err := u.repo.Create(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Update replaces a example.
//
// The read and the write run in one transaction so a concurrent delete between
// them cannot produce an update against a row that no longer exists.
func (u *exampleUsecase) Update(ctx context.Context, in UpdateInput) (*model.Example, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, httpx.BadRequest("name must not be empty")
	}

	var out *model.Example
	err := u.tx.WithTransaction(ctx, func(ctx context.Context) error {
		existing, err := u.repo.GetByID(ctx, in.ID)
		if err != nil {
			return err
		}

		existing.Name = name
		existing.Description = strings.TrimSpace(in.Description)

		if err := u.repo.Update(ctx, existing); err != nil {
			return err
		}
		out = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Patch applies only the fields the caller supplied.
func (u *exampleUsecase) Patch(ctx context.Context, in PatchInput) (*model.Example, error) {
	if in.Name == nil && in.Description == nil {
		return nil, httpx.BadRequest("no fields to update")
	}

	var out *model.Example
	err := u.tx.WithTransaction(ctx, func(ctx context.Context) error {
		existing, err := u.repo.GetByID(ctx, in.ID)
		if err != nil {
			return err
		}

		if in.Name != nil {
			name := strings.TrimSpace(*in.Name)
			if name == "" {
				return httpx.BadRequest("name must not be empty")
			}
			existing.Name = name
		}
		if in.Description != nil {
			existing.Description = strings.TrimSpace(*in.Description)
		}

		if err := u.repo.Update(ctx, existing); err != nil {
			return err
		}
		out = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete soft deletes a example.
func (u *exampleUsecase) Delete(ctx context.Context, id uint) error {
	if err := u.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete example: %w", err)
	}
	return nil
}
