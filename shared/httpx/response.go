package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/MAbduhI/go-boilerplate/shared/logger"
	"github.com/MAbduhI/go-boilerplate/shared/validation"
)

// Envelope is the single response shape every endpoint returns, so clients can
// parse success and failure with one type.
type Envelope struct {
	Success bool       `json:"success"`
	Message string     `json:"message"`
	Data    any        `json:"data,omitempty"`
	Error   *ErrorInfo `json:"error,omitempty"`
	Meta    *Meta      `json:"meta,omitempty"`
}

// ErrorInfo describes a failure without leaking internals.
type ErrorInfo struct {
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

// Meta carries pagination for list endpoints.
type Meta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
}

// NewMeta computes TotalPages from the page size, guarding a zero size.
func NewMeta(page, pageSize int, totalItems int64) *Meta {
	totalPages := 0
	if pageSize > 0 {
		totalPages = int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	}
	return &Meta{Page: page, PageSize: pageSize, TotalItems: totalItems, TotalPages: totalPages}
}

// JSON writes an arbitrary status with the standard envelope.
func JSON(c *gin.Context, status int, body Envelope) {
	c.JSON(status, body)
}

// OK writes 200 with data.
func OK(c *gin.Context, data any) {
	JSON(c, http.StatusOK, Envelope{Success: true, Message: "ok", Data: data})
}

// OKMessage writes 200 with a custom message.
func OKMessage(c *gin.Context, message string, data any) {
	JSON(c, http.StatusOK, Envelope{Success: true, Message: message, Data: data})
}

// List writes 200 with data and pagination metadata.
func List(c *gin.Context, data any, meta *Meta) {
	JSON(c, http.StatusOK, Envelope{Success: true, Message: "ok", Data: data, Meta: meta})
}

// Created writes 201 with the newly created resource.
func Created(c *gin.Context, data any) {
	JSON(c, http.StatusCreated, Envelope{Success: true, Message: "created", Data: data})
}

// NoContent writes 204 with an empty body.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Fail is the single error exit for handlers. It resolves the status from the
// error, logs internal failures with their cause, and returns a client-safe
// body.
//
// Anything that maps to 5xx is reported to the client as a generic message:
// the specific cause goes to the log, never over the wire.
func Fail(c *gin.Context, err error) {
	if err == nil {
		err = ErrInternalServer
	}
	status := StatusCode(err)
	code := Code(err)

	message := msgInternal
	var details map[string]any

	var domainErr *Error
	if errors.As(err, &domainErr) {
		if domainErr.Message != "" {
			message = domainErr.Message
		} else {
			message = domainErr.Kind.Error()
		}
		details = domainErr.Details
	} else if status < http.StatusInternalServerError {
		message = err.Error()
	}

	if status >= http.StatusInternalServerError {
		message = msgInternal
		details = nil
		logged := err
		if domainErr != nil && domainErr.Cause() != nil {
			logged = domainErr.Cause()
		}
		// Method and path are not repeated here: the request-scoped logger
		// installed by the RequestID middleware already carries them.
		logger.FromContext(c.Request.Context()).Error("request failed",
			slog.Int("status", status),
			slog.String("route", c.FullPath()),
			slog.String("error", logged.Error()))
	}

	// Record the error on the Gin context so middleware and access logs can
	// see it, then abort so no later handler writes a second body.
	_ = c.Error(err)
	c.AbortWithStatusJSON(status, Envelope{
		Success: false,
		Message: message,
		Error:   &ErrorInfo{Code: code, Details: details},
	})
}

// FailValidation reports a binding or validation failure as 422, with the
// failing fields named as they appear in the request JSON.
func FailValidation(c *gin.Context, err error) {
	Fail(c, New(ErrValidation, "request validation failed").
		WithDetails(map[string]any{"fields": validation.FieldErrors(err)}).
		Wrap(err))
}
