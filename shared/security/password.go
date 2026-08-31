// Package security holds framework-agnostic password hashing and JWT helpers.
// Nothing here imports Gin, so the same code serves HTTP handlers, gRPC
// servers and background workers.
package security

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/MAbduhI/go-boilerplate/shared/config"
)

// ErrPasswordMismatch reports a wrong password. Compare with errors.Is.
var ErrPasswordMismatch = bcrypt.ErrMismatchedHashAndPassword

// DefaultCost is the bcrypt cost used when BCRYPT_COST is unset.
//
// The reference project hardcoded 14, which takes roughly a second per hash on
// commodity hardware and turns the login endpoint into an easy CPU-exhaustion
// target. 12 keeps a strong margin over bcrypt's default of 10 while staying
// in the tens of milliseconds.
const DefaultCost = 12

// Hasher hashes and verifies passwords at a fixed cost.
type Hasher struct{ cost int }

// NewHasher returns a Hasher reading BCRYPT_COST from the environment.
func NewHasher() Hasher {
	return NewHasherWithCost(config.Int("BCRYPT_COST", DefaultCost))
}

// NewHasherWithCost clamps cost into the range bcrypt accepts.
func NewHasherWithCost(cost int) Hasher {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = DefaultCost
	}
	return Hasher{cost: cost}
}

// Cost reports the configured bcrypt cost.
func (h Hasher) Cost() int {
	if h.cost == 0 {
		return DefaultCost
	}
	return h.cost
}

// Hash returns the bcrypt hash of password.
//
// bcrypt silently truncates input at 72 bytes, so a longer password would
// validate against any string sharing its first 72 bytes; reject it instead.
func (h Hasher) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("security: password must not be empty")
	}
	if len(password) > 72 {
		return "", errors.New("security: password exceeds bcrypt's 72-byte limit")
	}
	out, err := bcrypt.GenerateFromPassword([]byte(password), h.Cost())
	if err != nil {
		return "", fmt.Errorf("security: hash password: %w", err)
	}
	return string(out), nil
}

// Verify reports whether password matches hash. It returns ErrPasswordMismatch
// on a wrong password and a different error if the stored hash is malformed —
// the two cases need different handling, so they are not collapsed into a bool.
func (h Hasher) Verify(password, hash string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return ErrPasswordMismatch
		}
		return fmt.Errorf("security: verify password: %w", err)
	}
	return nil
}

// NeedsRehash reports whether a stored hash was produced at a weaker cost than
// currently configured, so callers can upgrade it on next successful login.
func (h Hasher) NeedsRehash(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		return true
	}
	return cost < h.Cost()
}
