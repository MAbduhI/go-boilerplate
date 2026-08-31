// Package middleware holds the cross-cutting Gin middleware the API installs.
package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/MAbduhI/go-boilerplate/shared/httpx"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// RequestIDHeader is the header carrying the correlation id.
const RequestIDHeader = "X-Request-ID"

// requestIDKey is where the id lives on the Gin context.
const requestIDKey = "request_id"

// RequestID assigns every request a correlation id and attaches a logger
// carrying it to the request context.
//
// Putting the logger in the context here is what lets a repository deep in the
// call stack log with the request id without threading it through every
// signature.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}

		c.Set(requestIDKey, id)
		c.Header(RequestIDHeader, id)

		reqLogger := logger.L().With(
			slog.String("request_id", id),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
		)
		c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), reqLogger))

		c.Next()
	}
}

// RequestIDOf returns the correlation id assigned to c.
func RequestIDOf(c *gin.Context) string {
	if v, ok := c.Get(requestIDKey); ok {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// AccessLog logs one structured record per request.
//
// This replaces Gin's default logger, which writes unstructured text to stdout
// and so cannot be queried alongside the rest of the service's output.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		query := c.Request.URL.RawQuery

		c.Next()

		status := c.Writer.Status()
		attrs := []any{
			slog.Int("status", status),
			slog.String("client_ip", c.ClientIP()),
			slog.Duration("latency", time.Since(start)),
			slog.Int("bytes", c.Writer.Size()),
		}
		if query != "" {
			attrs = append(attrs, slog.String("query", query))
		}
		// Errors recorded via c.Error, including everything httpx.Fail reports.
		if errs := c.Errors.ByType(gin.ErrorTypeAny); len(errs) > 0 {
			attrs = append(attrs, slog.String("errors", errs.String()))
		}

		log := logger.FromContext(c.Request.Context())
		msg := "request completed"

		switch {
		case status >= 500:
			log.Error(msg, attrs...)
		case status >= 400:
			log.Warn(msg, attrs...)
		default:
			log.Info(msg, attrs...)
		}
	}
}

// Recovery converts a panic into a 500 through httpx, so a crashing handler
// returns the same envelope as any other failure instead of an empty body.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.FromContext(c.Request.Context()).Error("handler panicked",
			slog.Any("panic", recovered))
		httpx.Fail(c, httpx.ErrInternalServer)
	})
}

// CORSConfig configures the CORS middleware.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAge           time.Duration
}

// DefaultCORS returns a permissive configuration suited to development.
func DefaultCORS(origins []string) CORSConfig {
	return CORSConfig{
		AllowOrigins:  origins,
		AllowMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Accept", "Authorization", RequestIDHeader},
		ExposeHeaders: []string{RequestIDHeader},
		MaxAge:        12 * time.Hour,
	}
}

// CORS answers preflight requests and sets the response headers.
func CORS(cfg CORSConfig) gin.HandlerFunc {
	allowAll := len(cfg.AllowOrigins) == 1 && cfg.AllowOrigins[0] == "*"
	allowed := make(map[string]bool, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		allowed[o] = true
	}

	methods := strings.Join(cfg.AllowMethods, ", ")
	headers := strings.Join(cfg.AllowHeaders, ", ")
	exposed := strings.Join(cfg.ExposeHeaders, ", ")
	maxAge := int(cfg.MaxAge.Seconds())

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}

		switch {
		case allowAll && !cfg.AllowCredentials:
			// "*" cannot be combined with credentials, so it is only sent when
			// credentials are off.
			c.Header("Access-Control-Allow-Origin", "*")
		case allowAll || allowed[origin]:
			c.Header("Access-Control-Allow-Origin", origin)
			// Reflecting the origin makes the response origin-dependent, so it
			// must not be cached as if it were the same for everyone.
			c.Header("Vary", "Origin")
		default:
			// Unlisted origin: no CORS headers, and the browser blocks it.
			c.Next()
			return
		}

		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if exposed != "" {
			c.Header("Access-Control-Expose-Headers", exposed)
		}

		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", methods)
			c.Header("Access-Control-Allow-Headers", headers)
			if maxAge > 0 {
				c.Header("Access-Control-Max-Age", strconv.Itoa(maxAge))
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
