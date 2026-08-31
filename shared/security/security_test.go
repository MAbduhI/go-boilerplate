package security

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const testSecret = "a-test-secret-that-is-long-enough-for-hs256"

// hashing at the configured cost is slow by design; tests use the minimum.
func testHasher() Hasher { return NewHasherWithCost(bcrypt.MinCost) }

func TestHashAndVerify(t *testing.T) {
	h := testHasher()

	hash, err := h.Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if hash == "correct-horse-battery-staple" {
		t.Fatal("Hash returned the plaintext")
	}
	if err := h.Verify("correct-horse-battery-staple", hash); err != nil {
		t.Errorf("Verify(correct) = %v", err)
	}
	if err := h.Verify("wrong", hash); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("Verify(wrong) = %v, want ErrPasswordMismatch", err)
	}
}

// TestHashRejectsOverlongPassword covers bcrypt's 72-byte truncation, which would
// otherwise let any string sharing the first 72 bytes authenticate.
func TestHashRejectsOverlongPassword(t *testing.T) {
	h := testHasher()

	if _, err := h.Hash(strings.Repeat("a", 73)); err == nil {
		t.Fatal("expected an error for a password over bcrypt's 72-byte limit")
	}
	if _, err := h.Hash(strings.Repeat("a", 72)); err != nil {
		t.Errorf("72 bytes should be accepted, got %v", err)
	}
}

func TestHashRejectsEmptyPassword(t *testing.T) {
	if _, err := testHasher().Hash(""); err == nil {
		t.Fatal("expected an error for an empty password")
	}
}

// TestVerifyDistinguishesMalformedHash matters because a corrupted stored hash
// is an operational problem, not a wrong password.
func TestVerifyDistinguishesMalformedHash(t *testing.T) {
	err := testHasher().Verify("whatever", "not-a-bcrypt-hash")

	if err == nil {
		t.Fatal("expected an error for a malformed hash")
	}
	if errors.Is(err, ErrPasswordMismatch) {
		t.Error("a malformed hash should not be reported as a password mismatch")
	}
}

func TestNeedsRehash(t *testing.T) {
	weak, err := NewHasherWithCost(bcrypt.MinCost).Hash("pw")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if !NewHasherWithCost(12).NeedsRehash(weak) {
		t.Error("a hash made at MinCost should need rehashing at cost 12")
	}
	if NewHasherWithCost(bcrypt.MinCost).NeedsRehash(weak) {
		t.Error("a hash at the configured cost should not need rehashing")
	}
	if !NewHasherWithCost(12).NeedsRehash("garbage") {
		t.Error("an unreadable hash should be reported as needing a rehash")
	}
}

func TestNewHasherClampsInvalidCost(t *testing.T) {
	if got := NewHasherWithCost(99).Cost(); got != DefaultCost {
		t.Errorf("cost 99 should clamp to %d, got %d", DefaultCost, got)
	}
	if got := NewHasherWithCost(1).Cost(); got != DefaultCost {
		t.Errorf("cost 1 should clamp to %d, got %d", DefaultCost, got)
	}
}

func testManager(t *testing.T) *TokenManager {
	t.Helper()
	m, err := NewTokenManager(TokenConfig{
		Secret:     testSecret,
		Issuer:     "test",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return m
}

func TestNewTokenManagerRejectsShortSecret(t *testing.T) {
	if _, err := NewTokenManager(TokenConfig{Secret: "too-short"}); err == nil {
		t.Fatal("expected a short HS256 secret to be rejected")
	}
}

func TestGenerateAndVerifyAccess(t *testing.T) {
	m := testManager(t)

	token, exp, err := m.GenerateAccess("user-1", "session-1")
	if err != nil {
		t.Fatalf("GenerateAccess: %v", err)
	}
	if !exp.After(time.Now()) {
		t.Error("access token expiry should be in the future")
	}

	claims, err := m.VerifyAccess(token)
	if err != nil {
		t.Fatalf("VerifyAccess: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want user-1", claims.Subject)
	}
	if claims.Session != "session-1" {
		t.Errorf("Session = %q, want session-1", claims.Session)
	}
	if claims.Type != TokenAccess {
		t.Errorf("Type = %q, want access", claims.Type)
	}
}

// TestAccessTokenRejectedAtRefresh is the regression test for the reference
// project's copy-pasted VerifyRefresh, which accepted an access token and would
// trade it for a fresh long-lived pair.
func TestAccessTokenRejectedAtRefresh(t *testing.T) {
	m := testManager(t)

	access, _, err := m.GenerateAccess("user-1", "session-1")
	if err != nil {
		t.Fatalf("GenerateAccess: %v", err)
	}

	if _, err := m.VerifyRefresh(access); !errors.Is(err, ErrTokenWrongType) {
		t.Fatalf("VerifyRefresh(access token) = %v, want ErrTokenWrongType", err)
	}

	refresh, _, err := m.GenerateRefresh("user-1", "session-1")
	if err != nil {
		t.Fatalf("GenerateRefresh: %v", err)
	}
	if _, err := m.VerifyRefresh(refresh); err != nil {
		t.Errorf("VerifyRefresh(refresh token) = %v", err)
	}
	if _, err := m.VerifyAccess(refresh); !errors.Is(err, ErrTokenWrongType) {
		t.Errorf("VerifyAccess(refresh token) = %v, want ErrTokenWrongType", err)
	}
}

// TestVerifyRejectsExpiredToken checks that an expired token maps to the
// ErrTokenExpired sentinel rather than the generic ErrTokenInvalid, so a caller
// can tell "log in again" from "this token is bogus".
//
// NewTokenManager normalises a non-positive TTL, so the expired token is signed
// directly here rather than through GenerateAccess.
func TestVerifyRejectsExpiredToken(t *testing.T) {
	m := testManager(t)
	past := time.Now().Add(-2 * time.Hour)

	claims := Claims{
		Session: "session-1",
		Type:    TokenAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			Issuer:    "test",
			IssuedAt:  jwt.NewNumericDate(past),
			NotBefore: jwt.NewNumericDate(past),
			ExpiresAt: jwt.NewNumericDate(past.Add(time.Minute)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}

	if _, err := m.VerifyAccess(token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("VerifyAccess(expired) = %v, want ErrTokenExpired", err)
	}
}

// TestVerifyRejectsUnsignedToken covers the alg:none substitution attack, which
// the WithValidMethods parser option is there to stop.
func TestVerifyRejectsUnsignedToken(t *testing.T) {
	m := testManager(t)

	claims := Claims{
		Type: TokenAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "attacker",
			Issuer:    "test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none-alg token: %v", err)
	}

	if _, err := m.VerifyAccess(token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("VerifyAccess(alg:none) = %v, want ErrTokenInvalid", err)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	m := testManager(t)
	token, _, err := m.GenerateAccess("user-1", "s")
	if err != nil {
		t.Fatalf("GenerateAccess: %v", err)
	}

	other, err := NewTokenManager(TokenConfig{
		Secret: "a-completely-different-secret-of-sufficient-length",
		Issuer: "test",
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	if _, err := other.VerifyAccess(token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("verifying with the wrong key = %v, want ErrTokenInvalid", err)
	}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	m := testManager(t)
	token, _, err := m.GenerateAccess("user-1", "s")
	if err != nil {
		t.Fatalf("GenerateAccess: %v", err)
	}

	other, err := NewTokenManager(TokenConfig{Secret: testSecret, Issuer: "someone-else"})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if _, err := other.VerifyAccess(token); err == nil {
		t.Fatal("a token from another issuer should be rejected")
	}
}

func TestGenerateRejectsEmptySubject(t *testing.T) {
	if _, _, err := testManager(t).GenerateAccess("", "s"); err == nil {
		t.Fatal("expected an error for an empty subject")
	}
}

func TestGeneratePair(t *testing.T) {
	m := testManager(t)

	pair, err := m.GeneratePair("user-1", "session-1")
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}
	if pair.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", pair.TokenType)
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Error("access and refresh tokens should differ")
	}
	if !pair.RefreshExpiresAt.After(pair.AccessExpiresAt) {
		t.Error("the refresh token should outlive the access token")
	}
	if _, err := m.VerifyAccess(pair.AccessToken); err != nil {
		t.Errorf("VerifyAccess: %v", err)
	}
	if _, err := m.VerifyRefresh(pair.RefreshToken); err != nil {
		t.Errorf("VerifyRefresh: %v", err)
	}
}

// TestVerifyAcceptsAnyTypeWhenUnspecified documents the empty-want behaviour.
func TestVerifyAcceptsAnyTypeWhenUnspecified(t *testing.T) {
	m := testManager(t)
	refresh, _, err := m.GenerateRefresh("user-1", "s")
	if err != nil {
		t.Fatalf("GenerateRefresh: %v", err)
	}
	if _, err := m.Verify(refresh, ""); err != nil {
		t.Errorf("Verify with no required type = %v", err)
	}
}
