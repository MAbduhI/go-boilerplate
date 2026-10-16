package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrPKCEInvalidLength     = errors.New("security: code_verifier must be between 43 and 128 characters")
	ErrPKCEInvalidVerifier   = errors.New("security: code_verifier contains invalid unreserved characters")
	ErrPKCEUnsupportedMode   = errors.New("security: code_challenge_method must be S256 or plain")
	ErrPKCEChallengeMismatch = errors.New("security: code_verifier does not match code_challenge")
)

const (
	PKCEMethodS256  = "S256"
	PKCEMethodPlain = "plain"

	MinVerifierLength = 43
	MaxVerifierLength = 128
)

func GenerateCodeVerifier(length int) (string, error) {
	if length < MinVerifierLength || length > MaxVerifierLength {
		length = 64
	}
	byteCount := (length * 3) / 4
	b := make([]byte, byteCount)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("security: failed to generate random bytes: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(b)
	if len(encoded) > length {
		encoded = encoded[:length]
	}
	return encoded, nil
}

func ComputeCodeChallenge(verifier string, method string) (string, error) {
	if err := ValidateCodeVerifier(verifier); err != nil {
		return "", err
	}

	switch strings.ToUpper(method) {
	case PKCEMethodS256, "":
		h := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(h[:]), nil
	case PKCEMethodPlain:
		return verifier, nil
	default:
		return "", ErrPKCEUnsupportedMode
	}
}

func ValidateCodeVerifier(verifier string) error {
	if len(verifier) < MinVerifierLength || len(verifier) > MaxVerifierLength {
		return ErrPKCEInvalidLength
	}
	for i := 0; i < len(verifier); i++ {
		b := verifier[i]
		if isAllowed(b) {
			continue
		}
		return ErrPKCEInvalidVerifier
	}
	return nil
}

func isAllowed(b byte) bool {
	if (b >= "a"[0] && b <= "z"[0]) || (b >= "A"[0] && b <= "Z"[0]) || (b >= "0"[0] && b <= "9"[0]) {
		return true
	}
	return b == "-"[0] || b == "."[0] || b == "_"[0] || b == "~"[0]
}

func VerifyPKCE(verifier, challenge, method string) error {
	expectedChallenge, err := ComputeCodeChallenge(verifier, method)
	if err != nil {
		return err
	}

	if subtle.ConstantTimeCompare([]byte(expectedChallenge), []byte(challenge)) != 1 {
		return ErrPKCEChallengeMismatch
	}
	return nil
}
