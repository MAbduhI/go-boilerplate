package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/MAbduhI/go-boilerplate/shared/database/postgres"

	"github.com/MAbduhI/go-boilerplate/modules/example/repository"
	"github.com/MAbduhI/go-boilerplate/modules/example/usecase"
)

// Wire is this module's composition root: it builds the repository, usecase and
// handler and mounts the routes.
//
// Keeping the wiring inside the module is what lets `gen make:module` add a
// single line to the central router and have the result compile. The app does
// not need to know that a ExampleRepository exists, and the module's
// repository and usecase packages stay unreachable from outside.
func Wire(rg *gin.RouterGroup, db postgres.DB) *ExampleHandler {
	repo := repository.NewExampleRepository(db)
	uc := usecase.NewExampleUsecase(repo, db)

	h := NewHandler(uc)
	h.RegisterRoutes(rg)
	return h
}
