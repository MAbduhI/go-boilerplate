// Package router builds the HTTP engine and mounts every module's routes.
package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/database/postgres"
	"github.com/MAbduhI/go-boilerplate/shared/httpx"

	"github.com/MAbduhI/go-boilerplate/app/internal/middleware"

	// forge:imports:start — managed by `gen`, do not edit between the markers
	example "github.com/MAbduhI/go-boilerplate/modules/example/handler"
	// forge:imports:end
)

// Dependencies carries everything the modules need.
//
// DB is the only required field: `gen make:module` generates a module that wires
// itself from it, which is what keeps the generated router line to one entry.
// Add a field here when a module needs another connector, and pass it in that
// module's Wire call.
type Dependencies struct {
	DB postgres.DB
}

// New builds the Gin engine with the standard middleware stack.
func New(cfg config.App, deps *Dependencies) *gin.Engine {
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// gin.New rather than gin.Default: the default engine installs Gin's own
	// unstructured logger and recovery, which would duplicate and undercut the
	// structured middleware below.
	r := gin.New()

	r.Use(middleware.RequestID())
	r.Use(middleware.AccessLog())
	r.Use(middleware.Recovery())
	r.Use(middleware.CORS(middleware.DefaultCORS(cfg.AllowOrigins)))

	// Trusting every proxy would let any client spoof its IP via
	// X-Forwarded-For; set TRUSTED_PROXIES when running behind a real one.
	if proxies := config.Strings("TRUSTED_PROXIES", nil); len(proxies) > 0 {
		_ = r.SetTrustedProxies(proxies)
	} else {
		_ = r.SetTrustedProxies(nil)
	}

	r.NoRoute(func(c *gin.Context) {
		httpx.Fail(c, httpx.NotFound("route not found"))
	})
	r.NoMethod(func(c *gin.Context) {
		httpx.Fail(c, httpx.New(httpx.ErrBadRequest, "method not allowed"))
	})

	registerSystemRoutes(r, deps)
	RegisterRoutes(r, deps)

	return r
}

// registerSystemRoutes mounts liveness and readiness probes.
//
// They are separate on purpose: liveness answers "is the process up", so it must
// not touch the database, while readiness answers "can it serve traffic", so it
// must.
func registerSystemRoutes(r *gin.Engine, deps *Dependencies) {
	r.GET("/healthz", func(c *gin.Context) {
		httpx.OK(c, map[string]any{"status": "ok"})
	})

	r.GET("/readyz", func(c *gin.Context) {
		if deps.DB != nil {
			if err := deps.DB.Ping(c.Request.Context()); err != nil {
				httpx.Fail(c, httpx.New(httpx.ErrServiceUnavailable, "database is not reachable").Wrap(err))
				return
			}
		}
		httpx.OK(c, map[string]any{"status": "ready"})
	})
}

// RegisterRoutes mounts the feature modules under /api/v1.
func RegisterRoutes(r *gin.Engine, deps *Dependencies) {
	v1 := r.Group("/api/v1")

	v1.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	// forge:routes:start — managed by `gen`, do not edit between the markers
	example.Wire(v1, deps.DB)
	// forge:routes:end
}
