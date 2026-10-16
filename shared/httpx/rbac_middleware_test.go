package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/MAbduhI/go-boilerplate/shared/httpx"
	"github.com/MAbduhI/go-boilerplate/shared/security"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestTokenManager(t *testing.T) *security.TokenManager {
	tm, err := security.NewTokenManager(security.TokenConfig{
		Secret:    "a-very-secure-secret-key-at-least-32-chars-long",
		Issuer:    "test-iam",
		AccessTTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return tm
}

func TestAuthMiddleware_NoHeader(t *testing.T) {
	tm := setupTestTokenManager(t)
	r := gin.New()
	r.Use(httpx.RequireAuth(tm))
	r.GET("/protected", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	tm := setupTestTokenManager(t)
	token, _, err := tm.GenerateAccessWithClaims("usr-1", "sess-1", "org-1", []string{"editor"}, []string{"posts:read"})
	if err != nil {
		t.Fatalf("GenerateAccessWithClaims: %v", err)
	}

	r := gin.New()
	r.Use(httpx.RequireAuth(tm))
	r.GET("/protected", func(c *gin.Context) {
		claims, ok := httpx.GetClaims(c)
		if !ok || claims.Subject != "usr-1" {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRequirePermission_Enforcement(t *testing.T) {
	tm := setupTestTokenManager(t)
	// Token only has "hris:employee:read" and "billing:*"
	token, _, _ := tm.GenerateAccessWithClaims("usr-2", "sess-2", "org-1", []string{"staff"}, []string{"hris:employee:read", "billing:*"})

	r := gin.New()
	r.Use(httpx.RequireAuth(tm))

	r.GET("/hris/read", httpx.RequirePermission("hris:employee:read"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.POST("/hris/write", httpx.RequirePermission("hris:employee:write"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.GET("/billing/invoice", httpx.RequirePermission("billing:invoice:read"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// 1. Should succeed for hris:employee:read
	req1 := httptest.NewRequest(http.MethodGet, "/hris/read", nil)
	req1.Header.Set("Authorization", "Bearer "+token)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("expected 200 for hris:employee:read, got %d", w1.Code)
	}

	// 2. Should fail 403 for hris:employee:write
	req2 := httptest.NewRequest(http.MethodPost, "/hris/write", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("expected 403 for hris:employee:write, got %d", w2.Code)
	}

	// 3. Should succeed for billing:invoice:read via billing:* wildcard
	req3 := httptest.NewRequest(http.MethodGet, "/billing/invoice", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 for wildcard billing:*, got %d", w3.Code)
	}
}

func TestRequireRole_Enforcement(t *testing.T) {
	tm := setupTestTokenManager(t)
	tokenAdmin, _, _ := tm.GenerateAccessWithClaims("usr-admin", "sess-1", "org-1", []string{"admin"}, nil)
	tokenUser, _, _ := tm.GenerateAccessWithClaims("usr-regular", "sess-2", "org-1", []string{"viewer"}, nil)

	r := gin.New()
	r.Use(httpx.RequireAuth(tm))
	r.DELETE("/admin/purge", httpx.RequireRole("admin", "manager"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Admin succeeds
	reqAdmin := httptest.NewRequest(http.MethodDelete, "/admin/purge", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+tokenAdmin)
	wAdmin := httptest.NewRecorder()
	r.ServeHTTP(wAdmin, reqAdmin)
	if wAdmin.Code != http.StatusOK {
		t.Errorf("admin expected 200, got %d", wAdmin.Code)
	}

	// Viewer fails 403
	reqUser := httptest.NewRequest(http.MethodDelete, "/admin/purge", nil)
	reqUser.Header.Set("Authorization", "Bearer "+tokenUser)
	wUser := httptest.NewRecorder()
	r.ServeHTTP(wUser, reqUser)
	if wUser.Code != http.StatusForbidden {
		t.Errorf("viewer expected 403, got %d", wUser.Code)
	}
}
