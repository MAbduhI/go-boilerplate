package security

import (
	"testing"
)

func TestPKCEFlow(t *testing.T) {
	verifier, err := GenerateCodeVerifier(64)
	if err != nil {
		t.Fatalf("unexpected error generating verifier: %v", err)
	}

	if len(verifier) != 64 {
		t.Fatalf("expected verifier length 64, got %d", len(verifier))
	}

	challenge, err := ComputeCodeChallenge(verifier, PKCEMethodS256)
	if err != nil {
		t.Fatalf("unexpected error computing S256 challenge: %v", err)
	}

	if err := VerifyPKCE(verifier, challenge, PKCEMethodS256); err != nil {
		t.Fatalf("expected PKCE verification to succeed, got %v", err)
	}

	// Tampered verifier must fail
	if err := VerifyPKCE(verifier+"invalid", challenge, PKCEMethodS256); err == nil {
		t.Fatalf("expected PKCE verification to fail on tampered verifier")
	}
}
