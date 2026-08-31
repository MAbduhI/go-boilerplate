package httpx

import (
	"github.com/gin-gonic/gin"
)

// BindJSON binds and validates a JSON body using Gin's own binding tags,
// answering with a 422 and field detail on failure.
//
// Returns false when the response has already been written, so callers do:
//
//	var req CreateRequest
//	if !httpx.BindJSON(c, &req) {
//	    return
//	}
func BindJSON(c *gin.Context, dest any) bool {
	if err := c.ShouldBindJSON(dest); err != nil {
		FailValidation(c, err)
		return false
	}
	return true
}

// BindQuery binds query parameters using `form` and `binding` tags.
func BindQuery(c *gin.Context, dest any) bool {
	if err := c.ShouldBindQuery(dest); err != nil {
		FailValidation(c, err)
		return false
	}
	return true
}

// BindURI binds path parameters using `uri` tags.
func BindURI(c *gin.Context, dest any) bool {
	if err := c.ShouldBindUri(dest); err != nil {
		FailValidation(c, err)
		return false
	}
	return true
}

// Pagination is an embeddable query struct for list endpoints.
type Pagination struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=200"`
}

// Normalize fills defaults so callers never deal with a zero page size.
func (p *Pagination) Normalize() {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
}

// Offset returns the SQL offset for the current page.
func (p Pagination) Offset() int { return (p.Page - 1) * p.PageSize }

// Limit returns the SQL limit for the current page.
func (p Pagination) Limit() int { return p.PageSize }
