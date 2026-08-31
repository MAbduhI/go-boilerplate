package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestStatusCodeMapsEverySentinel pins the domain-error to HTTP-status table.
func TestStatusCodeMapsEverySentinel(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrBadRequest, http.StatusBadRequest, "BAD_REQUEST"},
		{ErrValidation, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{ErrUnauthorized, http.StatusUnauthorized, "UNAUTHORIZED"},
		{ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{ErrConflict, http.StatusConflict, "CONFLICT"},
		{ErrTooManyRequests, http.StatusTooManyRequests, "TOO_MANY_REQUESTS"},
		{ErrServiceUnavailable, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE"},
		{ErrInternalServer, http.StatusInternalServerError, "INTERNAL_ERROR"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			if got := StatusCode(tc.err); got != tc.status {
				t.Errorf("StatusCode = %d, want %d", got, tc.status)
			}
			if got := Code(tc.err); got != tc.code {
				t.Errorf("Code = %q, want %q", got, tc.code)
			}
		})
	}
}

// TestStatusCodeSeesThroughWrapping is the fix for the reference project's
// `switch err` comparison, which lost the match the moment an error was wrapped.
func TestStatusCodeSeesThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("loading stock: %w", fmt.Errorf("query failed: %w", ErrNotFound))

	if got := StatusCode(wrapped); got != http.StatusNotFound {
		t.Fatalf("StatusCode(wrapped ErrNotFound) = %d, want 404", got)
	}
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is should still match through two layers of wrapping")
	}
}

// TestErrorUnwrapsToItsKind lets repositories return a rich Error while callers
// keep matching on the sentinel.
func TestErrorUnwrapsToItsKind(t *testing.T) {
	err := NotFound("stock not found")

	if !errors.Is(err, ErrNotFound) {
		t.Fatal("errors.Is(Error, ErrNotFound) = false")
	}
	if got := StatusCode(err); got != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", got)
	}

	var domainErr *Error
	if !errors.As(err, &domainErr) {
		t.Fatal("errors.As should recover the *Error")
	}
	if domainErr.Message != "stock not found" {
		t.Errorf("Message = %q", domainErr.Message)
	}
}

// TestInternalKeepsTheCauseOutOfTheMessage checks the internal detail is
// retained for logging but is not part of the client-facing message.
func TestInternalKeepsTheCauseOutOfTheMessage(t *testing.T) {
	cause := errors.New(`pq: relation "stocks" does not exist`)
	err := Internal(cause)

	if err.Message != "internal server error" {
		t.Errorf("Message = %q, want a generic message", err.Message)
	}
	if !errors.Is(err.Cause(), cause) {
		t.Error("Cause() should return the wrapped internal error")
	}
	if StatusCode(err) != http.StatusInternalServerError {
		t.Error("Internal should map to 500")
	}
}

// TestContextErrorsMap covers cancellation and deadline, which otherwise fall
// through to a misleading 500.
func TestContextErrorsMap(t *testing.T) {
	if got := StatusCode(context.DeadlineExceeded); got != http.StatusGatewayTimeout {
		t.Errorf("DeadlineExceeded = %d, want 504", got)
	}
	if got := StatusCode(context.Canceled); got != 499 {
		t.Errorf("Canceled = %d, want 499", got)
	}
	if got := StatusCode(fmt.Errorf("fetch: %w", context.DeadlineExceeded)); got != http.StatusGatewayTimeout {
		t.Errorf("wrapped DeadlineExceeded = %d, want 504", got)
	}
}

func TestStatusCodeDefaultsTo500(t *testing.T) {
	if got := StatusCode(errors.New("something nobody classified")); got != http.StatusInternalServerError {
		t.Errorf("StatusCode(unknown) = %d, want 500", got)
	}
}

func TestWithDetails(t *testing.T) {
	err := New(ErrValidation, "bad input").WithDetails(map[string]any{"name": "is required"})

	if err.Details["name"] != "is required" {
		t.Errorf("Details = %v", err.Details)
	}
	if StatusCode(err) != http.StatusUnprocessableEntity {
		t.Error("ErrValidation should map to 422")
	}
}

// TestNewMeta covers the page arithmetic, including the zero-page-size guard
// that would otherwise divide by zero.
func TestNewMeta(t *testing.T) {
	for _, tc := range []struct {
		page, size int
		total      int64
		wantPages  int
	}{
		{1, 20, 0, 0},
		{1, 20, 1, 1},
		{1, 20, 20, 1},
		{1, 20, 21, 2},
		{2, 10, 95, 10},
		{1, 0, 50, 0}, // guarded: no division by zero
	} {
		meta := NewMeta(tc.page, tc.size, tc.total)
		if meta.TotalPages != tc.wantPages {
			t.Errorf("NewMeta(%d,%d,%d).TotalPages = %d, want %d",
				tc.page, tc.size, tc.total, meta.TotalPages, tc.wantPages)
		}
	}
}
