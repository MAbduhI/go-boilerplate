// Package handler exposes example over REST using Gin.
//
// Request shapes are declared here, not in model, because binding rules are a
// transport concern: the same usecase is reachable from gRPC or a worker with no
// HTTP tags involved.
package handler

import (
	"net/http"

	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/MAbduhI/go-boilerplate/shared/httpx"

	"github.com/MAbduhI/go-boilerplate/modules/example/usecase"
)

// ExampleHandler serves the example endpoints.
type ExampleHandler struct {
	usecase usecase.ExampleUsecase
}

// NewHandler returns a example handler.
func NewHandler(uc usecase.ExampleUsecase) *ExampleHandler {
	return &ExampleHandler{usecase: uc}
}

// RegisterRoutes mounts the example routes on rg.
func (h *ExampleHandler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/examples")
	g.GET("", h.List)
	g.GET("/:id", h.GetByID)
	g.POST("", h.Create)
	g.PUT("/:id", h.Update)
	g.PATCH("/:id", h.Patch)
	g.DELETE("/:id", h.Delete)
}

// listRequest is the query for GET /examples.
type listRequest struct {
	httpx.Pagination
	Search string `form:"search" binding:"omitempty,max=200"`
}

// createRequest is the body for POST /examples.
//
// Validation uses Gin's own binding tags; shared/validation is only needed for
// rules that span fields.
type createRequest struct {
	Name        string `json:"name" binding:"required,min=1,max=255"`
	Description string `json:"description" binding:"omitempty,max=2000"`
}

// updateRequest is the body for PUT /examples/:id, a full replacement.
type updateRequest struct {
	Name        string `json:"name" binding:"required,min=1,max=255"`
	Description string `json:"description" binding:"omitempty,max=2000"`
}

// patchRequest is the body for PATCH /examples/:id.
//
// Pointer fields are what make a partial update expressible: a nil pointer means
// the key was absent, which is different from a key sent as "".
type patchRequest struct {
	Name        *string `json:"name" binding:"omitempty,min=1,max=255"`
	Description *string `json:"description" binding:"omitempty,max=2000"`
}

// List handles GET /examples.
func (h *ExampleHandler) List(c *gin.Context) {
	var req listRequest
	if !httpx.BindQuery(c, &req) {
		return
	}
	req.Normalize()

	out, err := h.usecase.List(c.Request.Context(), usecase.ListInput{
		Page:     req.Page,
		PageSize: req.PageSize,
		Search:   req.Search,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.List(c, out.Items, httpx.NewMeta(req.Page, req.PageSize, out.Total))
}

// GetByID handles GET /examples/:id.
func (h *ExampleHandler) GetByID(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}

	out, err := h.usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

// Create handles POST /examples.
func (h *ExampleHandler) Create(c *gin.Context) {
	var req createRequest
	if !httpx.BindJSON(c, &req) {
		return
	}

	out, err := h.usecase.Create(c.Request.Context(), usecase.CreateInput{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, out)
}

// Update handles PUT /examples/:id.
func (h *ExampleHandler) Update(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	var req updateRequest
	if !httpx.BindJSON(c, &req) {
		return
	}

	out, err := h.usecase.Update(c.Request.Context(), usecase.UpdateInput{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

// Patch handles PATCH /examples/:id.
func (h *ExampleHandler) Patch(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	var req patchRequest
	if !httpx.BindJSON(c, &req) {
		return
	}

	out, err := h.usecase.Patch(c.Request.Context(), usecase.PatchInput{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

// Delete handles DELETE /examples/:id.
func (h *ExampleHandler) Delete(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}

	if err := h.usecase.Delete(c.Request.Context(), id); err != nil {
		httpx.Fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// parseID reads the :id path parameter, answering 400 on a malformed value.
// It returns ok=false once the error response has been written.
func (h *ExampleHandler) parseID(c *gin.Context) (uint, bool) {
	raw := c.Param("id")

	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		httpx.Fail(c, httpx.BadRequest("id must be a positive integer"))
		return 0, false
	}
	return uint(id), true
}
