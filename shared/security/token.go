package security

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/MAbduhI/go-boilerplate/shared/config"
)

// TokenType distinguishes an access token from a refresh token.
type TokenType string

const (
	TokenAccess  TokenType = "access"
	TokenRefresh TokenType = "refresh"
)

// Token errors, comparable with errors.Is.
var (
	ErrTokenInvalid   = errors.New("security: token is invalid")
	ErrTokenExpired   = errors.New("security: token is expired")
	ErrTokenWrongType = errors.New("security: token is of the wrong type")
)

// Claims is the JWT payload. Subject carries the user identifier and Session a
// per-login identifier that lets a single session be revoked without
// invalidating every token the user holds.
type Claims struct {
	Session     string    `json:"sid,omitempty"`
	TenantID    string    `json:"tid,omitempty"`
	Roles       []string  `json:"roles,omitempty"`
	Permissions []string  `json:"perms,omitempty"`
	Type        TokenType `json:"typ"`
	jwt.RegisteredClaims
}

// HasRole checks if the claims contain a given role or superadmin.
func (c *Claims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role || r == "superadmin" {
			return true
		}
	}
	return false
}

// HasPermission checks if the claims contain an exact permission or a wildcard.
func (c *Claims) HasPermission(perm string) bool {
	for _, p := range c.Permissions {
		if p == perm || p == "*" {
			return true
		}
		if strings.HasSuffix(p, ":*") {
			prefix := strings.TrimSuffix(p, ":*")
			if strings.HasPrefix(perm, prefix+":") {
				return true
			}
		}
	}
	return false
}

// TokenConfig configures the signing key, issuer and lifetimes.
type TokenConfig struct {
	Secret     string
	Issuer     string
	Audience   []string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// LoadTokenConfig reads TokenConfig from the environment. JWT_SECRET has no
// default: an unset signing key must fail loudly at boot, not fall back to a
// well-known placeholder the way the reference project's "your-secret-key" did.
func LoadTokenConfig() (TokenConfig, error) {
	secret, err := config.MustString("JWT_SECRET")
	if err != nil {
		return TokenConfig{}, err
	}
	return TokenConfig{
		Secret:     secret,
		Issuer:     config.String("JWT_ISSUER", config.String("APP_NAME", "go-boilerplate")),
		Audience:   config.Strings("JWT_AUDIENCE", nil),
		AccessTTL:  config.Duration("JWT_ACCESS_TTL", 15*time.Minute),
		RefreshTTL: config.Duration("JWT_REFRESH_TTL", 720*time.Hour),
	}, nil
}

// TokenManager issues and verifies HS256 JWTs.
type TokenManager struct {
	secret []byte
	cfg    TokenConfig
}

// NewTokenManager validates cfg and returns a ready manager. A short secret is
// rejected because HS256 security rests entirely on key length.
func NewTokenManager(cfg TokenConfig) (*TokenManager, error) {
	if len(cfg.Secret) < 32 {
		return nil, errors.New("security: JWT secret must be at least 32 bytes")
	}
	if cfg.AccessTTL <= 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL <= 0 {
		cfg.RefreshTTL = 720 * time.Hour
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "go-boilerplate"
	}
	return &TokenManager{secret: []byte(cfg.Secret), cfg: cfg}, nil
}

// Config exposes the resolved configuration, mainly so handlers can report
// expires_in without recomputing it.
func (m *TokenManager) Config() TokenConfig { return m.cfg }

// Pair is a freshly issued access/refresh pair.
type Pair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// GenerateAccess issues an access token for subject within session.
func (m *TokenManager) GenerateAccess(subject, session string) (string, time.Time, error) {
	return m.generate(subject, session, "", nil, nil, TokenAccess, m.cfg.AccessTTL)
}

// GenerateAccessWithClaims issues an access token with tenant, roles, and permissions.
func (m *TokenManager) GenerateAccessWithClaims(subject, session, tenantID string, roles, perms []string) (string, time.Time, error) {
	return m.generate(subject, session, tenantID, roles, perms, TokenAccess, m.cfg.AccessTTL)
}

// GenerateRefresh issues a refresh token for subject within session.
func (m *TokenManager) GenerateRefresh(subject, session string) (string, time.Time, error) {
	return m.generate(subject, session, "", nil, nil, TokenRefresh, m.cfg.RefreshTTL)
}

// GeneratePair issues both tokens for one login.
func (m *TokenManager) GeneratePair(subject, session string) (Pair, error) {
	return m.GeneratePairWithClaims(subject, session, "", nil, nil)
}

// GeneratePairWithClaims issues both tokens including tenant, roles, and permissions.
func (m *TokenManager) GeneratePairWithClaims(subject, session, tenantID string, roles, perms []string) (Pair, error) {
	access, accessExp, err := m.GenerateAccessWithClaims(subject, session, tenantID, roles, perms)
	if err != nil {
		return Pair{}, err
	}
	refresh, refreshExp, err := m.GenerateRefresh(subject, session)
	if err != nil {
		return Pair{}, err
	}
	return Pair{
		AccessToken:      access,
		RefreshToken:     refresh,
		TokenType:        "Bearer",
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
	}, nil
}

func (m *TokenManager) generate(subject, session, tenantID string, roles, perms []string, typ TokenType, ttl time.Duration) (string, time.Time, error) {
	if subject == "" {
		return "", time.Time{}, errors.New("security: token subject must not be empty")
	}
	now := time.Now().UTC()
	exp := now.Add(ttl)

	claims := Claims{
		Session:     session,
		TenantID:    tenantID,
		Roles:       roles,
		Permissions: perms,
		Type:        typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    m.cfg.Issuer,
			Audience:  m.cfg.Audience,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        session,
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("security: sign token: %w", err)
	}
	return signed, exp, nil
}

// VerifyAccess parses tokenString and requires an access token.
func (m *TokenManager) VerifyAccess(tokenString string) (*Claims, error) {
	return m.Verify(tokenString, TokenAccess)
}

// VerifyRefresh parses tokenString and requires a refresh token.
//
// The type check is the point: the reference project's VerifyRefresh was a byte
// copy of VerifyToken, so any access token was accepted at the refresh
// endpoint and could be traded for a fresh long-lived pair.
func (m *TokenManager) VerifyRefresh(tokenString string) (*Claims, error) {
	return m.Verify(tokenString, TokenRefresh)
}

// Verify parses and validates tokenString, requiring the given type. Pass an
// empty want to accept any type.
func (m *TokenManager) Verify(tokenString string, want TokenType) (*Claims, error) {
	opts := make([]jwt.ParserOption, 0, 3+len(m.cfg.Audience))
	opts = append(opts,
		// Pin the algorithm so a token with alg:none or an asymmetric alg can
		// never be substituted.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.cfg.Issuer),
		jwt.WithExpirationRequired(),
	)
	for _, aud := range m.cfg.Audience {
		opts = append(opts, jwt.WithAudience(aud))
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(*jwt.Token) (any, error) {
		return m.secret, nil
	}, opts...)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	if !token.Valid {
		return nil, ErrTokenInvalid
	}
	if want != "" && claims.Type != want {
		return nil, fmt.Errorf("%w: want %s, got %s", ErrTokenWrongType, want, claims.Type)
	}
	return claims, nil
}
