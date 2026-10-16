package httpx

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/MAbduhI/go-boilerplate/shared/security"
)

// ContextClaimsKey is the gin.Context key storing verified *security.Claims.
const ContextClaimsKey = "iam_claims"

// GetClaims retrieves verified JWT claims from the gin.Context.
func GetClaims(c *gin.Context) (*security.Claims, bool) {
	val, exists := c.Get(ContextClaimsKey)
	if !exists {
		return nil, false
	}
	claims, ok := val.(*security.Claims)
	return claims, ok
}

// RequireAuth validates the Authorization: Bearer <token> header against the TokenManager.
// Upon success, the parsed *security.Claims is attached to the request context.
func RequireAuth(tm *security.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":   "UNAUTHORIZED",
					"detail": "missing authorization header",
				},
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":   "UNAUTHORIZED",
					"detail": "invalid bearer token format",
				},
			})
			return
		}

		claims, err := tm.VerifyAccess(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":   "UNAUTHORIZED",
					"detail": "invalid or expired access token",
				},
			})
			return
		}

		c.Set(ContextClaimsKey, claims)
		c.Next()
	}
}

// RequirePermission checks whether current user possesses the required permission code or wildcard.
func RequirePermission(requiredPerm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := GetClaims(c)
		if !ok || claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":   "UNAUTHORIZED",
					"detail": "unauthenticated request",
				},
			})
			return
		}

		if claims.HasPermission(requiredPerm) {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"error": gin.H{
				"code":   "FORBIDDEN",
				"detail": "insufficient permissions: required " + requiredPerm,
			},
		})
	}
}

// RequireRole checks whether current user belongs to one of allowed roles or is superadmin.
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := GetClaims(c)
		if !ok || claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":   "UNAUTHORIZED",
					"detail": "unauthenticated request",
				},
			})
			return
		}

		for _, role := range allowedRoles {
			if claims.HasRole(role) {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"error": gin.H{
				"code":   "FORBIDDEN",
				"detail": "insufficient role privilege",
			},
		})
	}
}
