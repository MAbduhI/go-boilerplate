package security

import (
	"testing"
	"time"
)

func TestTokenManager_RBACClaims(t *testing.T) {
	mgr, err := NewTokenManager(TokenConfig{
		Secret:    "a-secret-string-that-is-at-least-32-chars-long",
		Issuer:    "iam-service",
		AccessTTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	userID := "usr-12345"
	sessionID := "sess-67890"
	tenantID := "tenant-alpha"
	roles := []string{"admin", "manager"}
	perms := []string{"hris:employee:read", "billing:invoice:*"}

	tokenStr, exp, err := mgr.GenerateAccessWithClaims(userID, sessionID, tenantID, roles, perms)
	if err != nil {
		t.Fatalf("GenerateAccessWithClaims: %v", err)
	}

	if exp.Before(time.Now()) {
		t.Errorf("expected expiration in future, got %v", exp)
	}

	claims, err := mgr.VerifyAccess(tokenStr)
	if err != nil {
		t.Fatalf("VerifyAccess: %v", err)
	}

	if claims.Subject != userID {
		t.Errorf("subject = %q, want %q", claims.Subject, userID)
	}
	if claims.TenantID != tenantID {
		t.Errorf("tenantID = %q, want %q", claims.TenantID, tenantID)
	}
	if len(claims.Roles) != 2 || claims.Roles[0] != "admin" {
		t.Errorf("roles = %v, want %v", claims.Roles, roles)
	}
	if len(claims.Permissions) != 2 || claims.Permissions[0] != "hris:employee:read" {
		t.Errorf("permissions = %v, want %v", claims.Permissions, perms)
	}

	// Test HasRole helper
	if !claims.HasRole("admin") {
		t.Errorf("expected HasRole('admin') == true")
	}
	if !claims.HasRole("manager") {
		t.Errorf("expected HasRole('manager') == true")
	}
	if claims.HasRole("staff") {
		t.Errorf("expected HasRole('staff') == false")
	}

	// Test HasPermission helper with exact and wildcard matches
	if !claims.HasPermission("hris:employee:read") {
		t.Errorf("expected HasPermission('hris:employee:read') == true")
	}
	if !claims.HasPermission("billing:invoice:create") {
		t.Errorf("expected HasPermission('billing:invoice:create') == true by wildcard")
	}
	if claims.HasPermission("hris:employee:delete") {
		t.Errorf("expected HasPermission('hris:employee:delete') == false")
	}
}
