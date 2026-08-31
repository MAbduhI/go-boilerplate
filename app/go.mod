module github.com/MAbduhI/go-boilerplate/app

go 1.26.5

// Sibling workspace modules (shared, modules/*) are resolved by go.work from
// disk and must not be listed here — see the note in modules/example/go.mod.
require (
	github.com/gin-gonic/gin v1.12.0
	github.com/google/uuid v1.6.0
	gorm.io/gorm v1.31.2
)
