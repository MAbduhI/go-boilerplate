# How to Use `go-boilerplate` — Developer Guide & Reference

Welcome to **`go-boilerplate`**, a production-grade Go backend boilerplate built with **Gin + GORM + PostgreSQL**, following **Clean Architecture**, and laid out as a **multi-module monorepo** driven by `go.work`.

This guide walks you through everything you need to know: project structure, architectural mental model, local setup, CLI code generation (`gen`), creating new features end-to-end, working with database transactions, connecting to infrastructure (Redis, MongoDB, Kafka, Elasticsearch, Temporal, etc.), gRPC, security, testing, and deployment.

---

## Table of Contents

1. [Project Structure](#1-project-structure)
2. [Architecture & Mental Model](#2-architecture--mental-model)
3. [Prerequisites & Development Tools](#3-prerequisites--development-tools)
4. [Quick Start & Local Environment](#4-quick-start--local-environment)
5. [The `gen` Code Generator CLI](#5-the-gen-code-generator-cli)
6. [End-to-End Feature Development Walkthrough](#6-end-to-end-feature-development-walkthrough)
7. [Working with Connectors & Infrastructure](#7-working-with-connectors--infrastructure)
8. [HTTP APIs, Envelopes & Error Handling (`httpx`)](#8-http-apis-envelopes--error-handling-httpx)
9. [Security & Authentication](#9-security--authentication)
10. [Database & Transactions](#10-database--transactions)
11. [gRPC Service Development](#11-grpc-service-development)
12. [Workspace Mechanics & Dependency Rules](#12-workspace-mechanics--dependency-rules)
13. [Testing, Linting & CI](#13-testing-linting--ci)
14. [Graceful Shutdown & Production Deployment](#14-graceful-shutdown--production-deployment)
15. [Troubleshooting & Common Pitfalls](#15-troubleshooting--common-pitfalls)
16. [Command Cheat Sheet](#16-command-cheat-sheet)

---

## 1. Project Structure

The repository is organized as a **multi-module monorepo** using Go Workspaces (`go.work`). Each top-level boundary (`app`, `cmd/gen`, `shared`, and each feature in `modules/*`) is its own Go module with its own `go.mod`.

```
go-boilerplate/
├── go.work                               # Go workspace definition coordinating all modules
├── .forge.yaml                           # Code generator (gen) config: module_prefix, base_model, paths
├── .golangci.yml                         # Workspace-wide linter rules (golangci-lint v2 schema)
├── docker-compose.yml                    # Local multi-service compose (Postgres, Redis, Mailpit, Mongo, etc.)
├── Makefile                              # Central task runner (build, test, lint, check, gen, proto, etc.)
├── .env.example                          # Canonical environment variable specifications & defaults
├── README.md                             # Repository overview and quick start
├── HOW_TO_USE.md                         # Detailed developer guide & usage manual
│
├── cmd/
│   └── gen/                              # Built-in code scaffolding CLI tool (own Go module)
│       ├── main.go                       # CLI entry point, flag parsing (--grpc, --base, --force, --dry-run)
│       ├── commands.go                   # Command definitions (make:module, make:model, make:usecase, etc.)
│       ├── forge.go                      # .forge.yaml parser & workspace root discovery
│       ├── naming.go                     # Linguistic inflection: camelCase, PascalCase, snake_case, plurals
│       ├── render.go                     # Go text/template rendering with formatting
│       ├── router.go                     # Central router marker inspection & line injection
│       ├── gowork.go                     # Automated `go work use` registration
│       ├── gomod.go                      # go.mod dependency inspector and resolver
│       ├── connector.go                  # Shared connector scaffolding registry
│       ├── grpc.go                       # Protobuf scaffolding & compilation runner (buf / protoc)
│       ├── output.go                     # Styled terminal output logging (colored sections, warnings, errors)
│       └── templates/                    # Embedded code templates
│           ├── module/                   # model, repository, usecase, handler, wire, go.mod
│           ├── grpc/                     # service.proto, server stub
│           └── connector/                # redis, mongo, cassandra, dynamodb, elasticsearch, kafka, etc.
│
├── app/                                  # Application entry point & core HTTP server (own Go module)
│   ├── go.mod                            # App module definitions
│   ├── cmd/
│   │   └── api/
│   │       └── main.go                   # Process bootstrapper: env loading, logging, connectors, graceful shutdown
│   └── internal/
│       ├── router/
│       │   └── router.go                 # Gin engine instantiation, middleware mounting, health/readiness, route registry
│       └── middleware/
│           └── middleware.go             # RequestID (correlation), structured AccessLog, Recovery, CORS
│
├── modules/                              # Feature / Domain modules (one Go module EACH)
│   └── example/                          # Reference feature module
│       ├── go.mod                        # Module boundary: encapsulates private layers from sibling modules
│       ├── model/
│       │   └── example.go                # Domain entity embedding BaseModel
│       ├── repository/
│       │   └── example.go                # Data persistence layer using GORM & transaction-aware Db(ctx)
│       ├── usecase/
│       │   └── example.go                # Domain business logic with consumer-declared txManager interface
│       ├── handler/
│       │   ├── example.go                # Transport layer: Gin REST endpoints, request binding & validation
│       │   └── wire.go                   # Module composition root: constructs repo -> usecase -> handler
│       └── grpc/
│           ├── example.proto             # Protobuf service contract
│           └── server.go                 # gRPC server implementation stub
│
└── shared/                               # Cross-cutting foundational library (own Go module)
    ├── go.mod
    ├── config/
    │   ├── config.go                     # App config struct (ports, timeouts, logging)
    │   └── env.go                        # Zero-dependency .env parser and typed getters
    ├── logger/
    │   └── logger.go                     # Structured logger built on log/slog with context propagation
    ├── httpx/
    │   ├── response.go                   # Standard API envelope (success, message, data, error, meta)
    │   ├── error.go                      # Error classification sentinels and HTTP mapping
    │   └── bind.go                       # Request binding (JSON, Query, URI) & pagination helpers
    ├── validation/
    │   └── validation.go                 # Gin validator extension: JSON field name mapping & custom tags
    ├── security/
    │   ├── password.go                   # bcrypt hashing with cost management & 72-byte truncation safety
    │   └── token.go                      # HS256 JWT TokenManager with distinct Access & Refresh token validation
    ├── shutdown/
    │   └── shutdown.go                   # Thread-safe LIFO resource shutdown registry
    ├── database/
    │   ├── model/
    │   │   ├── base.go                   # BaseModel (uint ID, Timestamps, soft-delete *time.Time)
    │   │   └── base_uuid.go              # BaseModelUUID (UUIDv7 ID, Timestamps, soft-delete *time.Time)
    │   ├── postgres/
    │   │   ├── postgres.go               # GORM client wrapper, connection pool configuration, scopes
    │   │   ├── context.go                # Ambient transaction context storage with unexported private key
    │   │   └── config.go                 # POSTGRES_* environment configuration loader
    │   └── nosql/
    │       ├── contract.go               # Unified NoSQLStore document interface (Find, Insert, Update, Delete)
    │       ├── codec.go                  # Struct-to-map BSON/JSON tag serializer/deserializer
    │       ├── mongo/                    # MongoDB driver implementation of NoSQLStore
    │       ├── cassandra/                # Apache Cassandra (gocql) implementation of NoSQLStore
    │       └── dynamodb/                 # AWS DynamoDB SDK v2 implementation of NoSQLStore
    ├── cache/
    │   └── redis/
    │       └── redis.go                  # go-redis client with default TTL, JSON caching, and Remember pattern
    ├── search/
    │   └── elasticsearch/
    │       └── elasticsearch.go          # Official Elasticsearch v8 client (Index, Get, Search, Bulk)
    ├── mail/
    │   └── mailpit/
    │       └── mailpit.go                # SMTP email delivery client with HTML/Text multipart support
    └── messaging/
        ├── kafka/
        │   └── kafka.go                  # segmentio/kafka-go pure-Go Producer and Consumer
        ├── temporal/
        │   └── temporal.go               # Temporal SDK client and background Worker runner
        └── rabbitmq/
            └── contract.go               # AMQP Publisher/Consumer/Broker interfaces
```

---

## 2. Architecture & Mental Model

### Clean Architecture Layering

The codebase strictly enforces unidirectional dependencies:

```
handler (Gin HTTP / Transport)
   ↓
usecase (Business Rules & Orchestration)
   ↓
repository (Persistence & Data Access / GORM)
   ↓
model (Pure Domain Structs)
```

| Layer | Location | Responsibilities & Rules | Allowed Imports | Forbidden Imports |
|---|---|---|---|---|
| **Handler** | `modules/<feature>/handler/` | Transport parsing, Gin binding, calling usecase, returning `httpx` envelopes | Usecase, `shared/httpx`, Gin | GORM, SQL, DB clients |
| **Usecase** | `modules/<feature>/usecase/` | Core business logic, validation, transaction boundaries | Repository interfaces, `model`, `shared/httpx` | GORM, Gin, SQL, `*gorm.DB` |
| **Repository** | `modules/<feature>/repository/` | Database queries, SQL mapping, translating driver errors to domain sentinels | `model`, `shared/database/postgres`, GORM | Gin, HTTP packages |
| **Model** | `modules/<feature>/model/` | Pure domain entities and base models | Go standard library, base models | GORM types (e.g. `gorm.DeletedAt`), Gin |

### Multi-Module Monorepo Isolation

Instead of a single monolithic `go.mod`, the codebase uses Go Workspaces (`go.work`):
- `app/` — Root application binary, central router, middleware, and lifecycle bootstrap.
- `cmd/gen/` — Built-in code generator binary.
- `shared/` — Reusable building blocks: database, config, security, logger, connectors, shutdown, httpx.
- `modules/<feature>/` — **One Go module per feature domain** (e.g. `modules/example`, `modules/stock`).

> [!IMPORTANT]
> **Structural Privacy:** Because `modules/<feature>` has its own `go.mod`, Go's compiler prevents sibling modules from importing its private `repository` or `usecase`. A module is reachable only through its handler composition root (`Wire`).

---

## 3. Prerequisites & Development Tools

### Required Dependencies
- **Go**: `1.24+` (or `1.26+`)
- **Docker & Docker Compose**: For running local database, cache, and messaging services
- **Make**: For running unified workspace commands

### Toolchain Installation

Install all required tools (`golangci-lint`, `protoc` Go plugins, and the `gen` generator CLI) with:

```bash
make tools
```

To install or update just the `gen` scaffolding CLI:

```bash
make gen
```

> [!TIP]
> Ensure your `$(go env GOPATH)/bin` directory is in your shell's `$PATH` so you can run `gen` and `golangci-lint` directly.

---

## 4. Quick Start & Local Environment

### Step 1: Configure Environment Variables

Copy the template `.env.example` to `.env`:

```bash
cp .env.example .env
```

Open `.env` and set a random 32-byte secret for `JWT_SECRET` (the application refuses to boot if unset):

```env
JWT_SECRET=your-secure-random-secret-at-least-32-characters-long
```

### Step 2: Start Local Dev Services

Start PostgreSQL, Redis, and Mailpit:

```bash
make up
```

### Step 3: Run the API

```bash
make run
```

### Step 4: Verify the Application

Open another terminal and test the endpoints:

```bash
# 1. Check Liveness (Process is running)
curl http://localhost:8080/healthz
# Response: {"success":true,"message":"ok","data":{"status":"ok"}}

# 2. Check Readiness (Postgres connection is healthy)
curl http://localhost:8080/readyz
# Response: {"success":true,"message":"ok","data":{"status":"ready"}}

# 3. Test Example Feature Module
curl http://localhost:8080/api/v1/examples
# Response: {"success":true,"message":"ok","data":[],"meta":{"page":1,"page_size":20,"total_items":0,"total_pages":0}}
```

To view outbound emails sent via Mailpit, open `http://localhost:8025` in your browser.

---

## 5. The `gen` Code Generator CLI

The `gen` CLI scaffolds complete feature modules, standalone layers, gRPC stubs, and shared connectors. All templates are embedded directly inside the binary.

### Command Reference

| Command | Description | What It Generates |
|---|---|---|
| `gen make:module <entity>` | Complete feature module | `model`, `repository`, `usecase`, `handler`, `wire.go`, `go.mod`, registers route in central router, runs `go work use` |
| `gen make:module <entity> --grpc` | Module with gRPC | Same as above + `.proto` file and Go gRPC server stub |
| `gen make:module <entity> --base BaseModelUUID` | Module with UUIDv7 PK | Uses UUIDv7 primary key instead of auto-incrementing integer |
| `gen make:model <entity>` | Scaffolds domain model only | `modules/<entity>/model/<entity>.go` |
| `gen make:repository <entity>` | Scaffolds repository only | `modules/<entity>/repository/<entity>.go` |
| `gen make:usecase <entity>` | Scaffolds generic CRUD usecase | `modules/<entity>/usecase/<entity>.go` |
| `gen make:usecase <entity> <Method>` | Scaffolds custom usecase method | `modules/<entity>/usecase/<entity>_<method>.go` |
| `gen make:handler <entity>` | Scaffolds Gin handler only | `modules/<entity>/handler/<entity>.go` |
| `gen make:grpc <entity>` | Scaffolds gRPC skeleton | `modules/<entity>/grpc/<entity>.proto` + `server.go` |
| `gen proto:build` | Compiles all proto files | Compiles `modules/*/grpc/*.proto` into Go code using `buf` or `protoc` |
| `gen make:connector <name>` | Scaffolds shared connector | Scaffolds connector files in `shared/` (`redis`, `mongo`, `cassandra`, `dynamodb`, `elasticsearch`, `kafka`, `temporal`, `rabbitmq`, `mailpit`) |

### Flags

- `--grpc`: Include gRPC service and stub during `make:module`.
- `--base <Type>`: Specify base model (`BaseModel` or `BaseModelUUID`).
- `--force` / `-f`: Overwrite existing files (default skips existing files).
- `--dry-run` / `-n`: Preview actions and files without modifying disk.

### Naming Conventions

The generator inflects entity names automatically across all Go identifiers, files, tables, and URL routes:

| Input Example | Package / Directory | Struct / Type | Table Name | REST Route Prefix |
|---|---|---|---|---|
| `stock` | `modules/stock` | `Stock` | `stocks` | `/api/v1/stocks` |
| `user-profile` | `modules/userprofile` | `UserProfile` | `user_profiles` | `/api/v1/user-profiles` |
| `category` | `modules/category` | `Category` | `categories` | `/api/v1/categories` |
| `api_key` | `modules/apikey` | `APIKey` | `api_keys` | `/api/v1/api-keys` |

### How Router Auto-Wiring Works

When `gen make:module` runs, it updates `app/internal/router/router.go` safely using deterministic comment markers:

```go
import (
    // forge:imports:start — managed by `gen`, do not edit between the markers
    example "github.com/MAbduhI/go-boilerplate/modules/example/handler"
    stock "github.com/MAbduhI/go-boilerplate/modules/stock/handler"
    // forge:imports:end
)

func RegisterRoutes(r *gin.Engine, deps *Dependencies) {
    v1 := r.Group("/api/v1")
    // forge:routes:start — managed by `gen`, do not edit between the markers
    example.Wire(v1, deps.DB)
    stock.Wire(v1, deps.DB)
    // forge:routes:end
}
```

---

## 6. End-to-End Feature Development Walkthrough

Let's walk through creating a complete `product` feature module from start to finish.

### Step 1: Scaffold the Module

```bash
gen make:module product
```

Output:
```
==> Scaffolding module product
  ✓ created modules/product/go.mod
  ✓ created modules/product/model/product.go
  ✓ created modules/product/repository/product.go
  ✓ created modules/product/usecase/product.go
  ✓ created modules/product/handler/product.go
  ✓ created modules/product/handler/wire.go
==> Wiring
  ✓ go work use ./modules/product
  ✓ wired product "github.com/MAbduhI/go-boilerplate/modules/product/handler" into the imports block
  ✓ wired product.Wire(v1, deps.DB) into the routes block
```

### Step 2: Define Domain Model (`modules/product/model/product.go`)

Add fields and GORM column tags to your model struct. Embed `base.BaseModel` (for `uint` ID) or `base.BaseModelUUID` (for UUIDv7):

```go
package model

import (
	base "github.com/MAbduhI/go-boilerplate/shared/database/model"
)

type Product struct {
	base.BaseModel

	SKU         string  `json:"sku" gorm:"type:varchar(64);uniqueIndex;not null"`
	Name        string  `json:"name" gorm:"type:varchar(255);not null;index"`
	Description string  `json:"description" gorm:"type:text"`
	Price       float64 `json:"price" gorm:"type:decimal(10,2);not null"`
	Stock       int     `json:"stock" gorm:"type:integer;not null;default:0"`
}

func (Product) TableName() string { return "products" }
```

> [!NOTE]
> `BaseModel` defines `DeletedAt *time.Time` (standard library type). Soft-delete filtering is handled explicitly via GORM scopes (`postgres.NotDeleted`).

### Step 3: Implement Repository Queries (`modules/product/repository/product.go`)

The repository uses `r.db.Db(ctx)` so every query automatically joins ambient transactions when present:

```go
func (r *productRepository) GetBySKU(ctx context.Context, sku string) (*model.Product, error) {
	var out model.Product
	err := r.db.Db(ctx).
		Scopes(postgres.NotDeleted).
		First(&out, "sku = ?", sku).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, httpx.NotFound("product not found")
		}
		return nil, fmt.Errorf("get product by sku %s: %w", sku, err)
	}
	return &out, nil
}
```

### Step 4: Add Custom Business Logic to Usecase

Need a specialized business operation like `AdjustStock`? Use `gen make:usecase`:

```bash
gen make:usecase product AdjustStock
```

This creates `modules/product/usecase/product_adjust_stock.go`:

```go
package usecase

import (
	"context"
	"github.com/MAbduhI/go-boilerplate/shared/httpx"
)

type ProductAdjustStockInput struct {
	ID       uint
	Quantity int
}

type ProductAdjustStockOutput struct {
	NewStock int
}

func (u *productUsecase) AdjustStock(ctx context.Context, in ProductAdjustStockInput) (ProductAdjustStockOutput, error) {
	var out ProductAdjustStockOutput

	// Run read and write atomically inside a transaction
	err := u.tx.WithTransaction(ctx, func(ctx context.Context) error {
		p, err := u.repo.GetByID(ctx, in.ID)
		if err != nil {
			return err
		}

		if p.Stock+in.Quantity < 0 {
			return httpx.BadRequest("insufficient stock")
		}

		p.Stock += in.Quantity
		if err := u.repo.Update(ctx, p); err != nil {
			return err
		}

		out.NewStock = p.Stock
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}
```

Add `AdjustStock` to the `ProductUsecase` interface in `modules/product/usecase/product.go`:

```go
type ProductUsecase interface {
	List(ctx context.Context, in ListInput) (ListOutput, error)
	GetByID(ctx context.Context, id uint) (*model.Product, error)
	Create(ctx context.Context, in CreateInput) (*model.Product, error)
	Update(ctx context.Context, in UpdateInput) (*model.Product, error)
	Patch(ctx context.Context, in PatchInput) (*model.Product, error)
	Delete(ctx context.Context, id uint) error
	AdjustStock(ctx context.Context, in ProductAdjustStockInput) (ProductAdjustStockOutput, error)
}
```

### Step 5: Expose Endpoint in Handler (`modules/product/handler/product.go`)

Mount the route and bind the request body:

```go
// 1. In RegisterRoutes:
func (h *ProductHandler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/products")
	g.GET("", h.List)
	g.GET("/:id", h.GetByID)
	g.POST("", h.Create)
	g.PUT("/:id", h.Update)
	g.PATCH("/:id", h.Patch)
	g.DELETE("/:id", h.Delete)
	g.POST("/:id/adjust-stock", h.AdjustStock)
}

// 2. Request struct:
type adjustStockRequest struct {
	Quantity int `json:"quantity" binding:"required"`
}

// 3. Handler method:
func (h *ProductHandler) AdjustStock(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	var req adjustStockRequest
	if !httpx.BindJSON(c, &req) {
		return
	}

	out, err := h.usecase.AdjustStock(c.Request.Context(), usecase.ProductAdjustStockInput{
		ID:       id,
		Quantity: req.Quantity,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}
```

### Step 6: Test Your Endpoints

Restart the API (`make run`) and test:

```bash
# Create a product
curl -X POST http://localhost:8080/api/v1/products \
  -H "Content-Type: application/json" \
  -d '{"name":"Mechanical Keyboard","description":"Hot-swappable switches"}'

# List products
curl http://localhost:8080/api/v1/products
```

---

## 7. Working with Connectors & Infrastructure

All connector wrappers live under `shared/` and are pre-built or generated via `gen make:connector <name>`.

### Enabling a Connector in `app/cmd/api/main.go`

Only Postgres is wired by default. To enable another connector (e.g. Redis, MongoDB, Elasticsearch, Kafka):

1. Open `app/cmd/api/main.go`.
2. Uncomment the connector import at the top.
3. In `openConnectors()`, uncomment the connector block and its shutdown registration.

Example: **Enabling Redis**:

```go
// 1. Uncomment import in app/cmd/api/main.go
import (
    redisclient "github.com/MAbduhI/go-boilerplate/shared/cache/redis"
)

// 2. Uncomment block in openConnectors()
func openConnectors(ctx context.Context, registry *shutdown.Registry) (*router.Dependencies, error) {
    deps := &router.Dependencies{}

    // Postgres
    db, err := postgres.New(ctx, postgres.LoadConfig())
    if err != nil { return nil, err }
    registry.RegisterErrFunc("postgres", db.Close)
    deps.DB = db

    // Redis
    rdb, err := redisclient.New(ctx, redisclient.LoadConfig())
    if err != nil { return nil, err }
    registry.RegisterErrFunc("redis", rdb.Close)
    deps.Redis = rdb

    return deps, nil
}
```

Pass the new dependency to `router.Dependencies` in `app/internal/router/router.go`:

```go
type Dependencies struct {
    DB    postgres.DB
    Redis *redisclient.Client
}
```

### Connector Capabilities Summary

| Connector | Package | Key Functions & Patterns |
|---|---|---|
| **Redis** | `shared/cache/redis` | `Set(ctx, key, val, ttl)`, `Get(ctx, key)`, `SetJSON`, `GetJSON`, `Remember(ctx, key, dest, ttl, computeFn)` |
| **NoSQL (Mongo, Cassandra, DynamoDB)** | `shared/database/nosql` | Common `nosql.NoSQLStore` contract: `FindOne`, `Find`, `InsertOne`, `UpdateOne`, `DeleteOne`, `Count` |
| **Elasticsearch** | `shared/search/elasticsearch` | Official v8 client: `Search(ctx, index, queryMap)`, `Index(ctx, index, id, doc)`, `Bulk(ctx, items)` |
| **Kafka** | `shared/messaging/kafka` | Pure Go client: `NewProducer(cfg).Publish(ctx, msg)`, `NewConsumer(cfg, topic).Consume(ctx, handler)` |
| **Temporal** | `shared/messaging/temporal` | Workflow orchestrator: `NewWorker(queue)`, `RegisterWorkflow`, `RegisterActivity`, `client.ExecuteWorkflow` |
| **Mailpit** | `shared/mail/mailpit` | Dev email catcher (SMTP): `mailer.Send(ctx, mailpit.Message{To, Subject, HTMLBody, TextBody})` |
| **RabbitMQ** | `shared/messaging/rabbitmq` | Clean messaging interfaces: `Publisher`, `Consumer`, `Broker` |

### Docker Compose Service Profiles

Start only the services needed for your current task:

```bash
docker compose up -d                        # Core services: Postgres, Redis, Mailpit
docker compose --profile nosql  up -d       # MongoDB, Cassandra, DynamoDB
docker compose --profile search up -d       # Elasticsearch 8.15
docker compose --profile queue  up -d       # Kafka (KRaft), Temporal + Temporal Web UI (:8088)
docker compose --profile all    up -d       # All services
```

---

## 8. HTTP APIs, Envelopes & Error Handling (`httpx`)

Every endpoint adheres to a standardized JSON response envelope.

### Response Formats

#### Success Envelope
```json
{
  "success": true,
  "message": "ok",
  "data": { "id": 1, "name": "Widget" },
  "meta": {
    "page": 1,
    "page_size": 20,
    "total_items": 100,
    "total_pages": 5
  }
}
```

#### Error Envelope
```json
{
  "success": false,
  "message": "product not found",
  "error": {
    "code": "NOT_FOUND",
    "details": null
  }
}
```

### Handler Response Helpers

In your handlers, return standard responses with `shared/httpx`:

```go
httpx.OK(c, data)                          // 200 OK
httpx.OKMessage(c, "custom message", data) // 200 OK with custom message
httpx.Created(c, data)                     // 201 Created
httpx.List(c, items, meta)                 // 200 OK with pagination metadata
httpx.NoContent(c)                         // 204 No Content
httpx.Fail(c, err)                         // Automatically maps err to status & error code
```

### Domain Error Sentinels & Status Codes

Usecases and repositories return standard error sentinels without importing HTTP packages:

| Sentinel Error | HTTP Status | Code String | Helper Constructor |
|---|---|---|---|
| `httpx.ErrNotFound` | `404 Not Found` | `NOT_FOUND` | `httpx.NotFound("message")` |
| `httpx.ErrBadRequest` | `400 Bad Request` | `BAD_REQUEST` | `httpx.BadRequest("message")` |
| `httpx.ErrValidation` | `422 Unprocessable`| `VALIDATION_FAILED` | `httpx.FailValidation(c, err)` |
| `httpx.ErrConflict` | `409 Conflict` | `CONFLICT` | `httpx.Conflict("message")` |
| `httpx.ErrUnauthorized` | `401 Unauthorized` | `UNAUTHORIZED` | `httpx.Unauthorized("message")` |
| `httpx.ErrForbidden` | `403 Forbidden` | `FORBIDDEN` | `httpx.Forbidden("message")` |
| `httpx.ErrTooManyRequests`| `429 Too Many` | `TOO_MANY_REQUESTS`| `httpx.New(httpx.ErrTooManyRequests, "...")` |
| `httpx.ErrInternalServer` | `500 Internal` | `INTERNAL_ERROR` | `httpx.Internal(err)` |

> [!IMPORTANT]
> **5xx Information Security:** Whenever an internal error occurs (500), `httpx.Fail` logs the real root cause using `slog`, but returns a sanitized `"internal server error"` message over the wire so internal details never leak to clients.

### Request Binding & Validation

Use `httpx.BindJSON`, `httpx.BindQuery`, and `httpx.BindURI`:

```go
type CreateUserRequest struct {
    Email    string `json:"email" binding:"required,email"`
    Username string `json:"username" binding:"required,min=3,max=30"`
    Role     string `json:"role" binding:"required,oneof=admin member guest"`
}

func (h *UserHandler) Create(c *gin.Context) {
    var req CreateUserRequest
    if !httpx.BindJSON(c, &req) {
        return // Automatically writes 422 with per-field errors mapped to JSON keys
    }
    // ... proceed to usecase
}
```

---

## 9. Security & Authentication

The `shared/security` package provides password hashing and JWT token issuance.

### Password Hashing (`security.Hasher`)

```go
hasher := security.NewHasher() // Reads BCRYPT_COST from env (default 12)

// Hash a password
hash, err := hasher.Hash("user-plain-password")

// Verify password
err = hasher.Verify("user-plain-password", hash)
if errors.Is(err, security.ErrPasswordMismatch) {
    // Wrong password
}

// Check if hash needs upgrading
if hasher.NeedsRehash(hash) {
    newHash, _ := hasher.Hash(password)
    // Save updated hash
}
```

### JWT Token Management (`security.TokenManager`)

```go
cfg, err := security.LoadTokenConfig()
tm, err := security.NewTokenManager(cfg)

// 1. Issue Access + Refresh Token Pair
pair, err := tm.GeneratePair(userID, sessionID)
// pair.AccessToken, pair.RefreshToken, pair.AccessExpiresAt, pair.RefreshExpiresAt

// 2. Verify Access Token
claims, err := tm.VerifyAccess(tokenString)
if errors.Is(err, security.ErrTokenExpired) { ... }
if errors.Is(err, security.ErrTokenInvalid) { ... }

// 3. Verify Refresh Token (strictly checks typ == "refresh")
refreshClaims, err := tm.VerifyRefresh(refreshTokenString)
```

### Example Authentication Middleware

```go
func AuthMiddleware(tm *security.TokenManager) gin.HandlerFunc {
    return func(c *gin.Context) {
        authHeader := c.GetHeader("Authorization")
        if !strings.HasPrefix(authHeader, "Bearer ") {
            httpx.Fail(c, httpx.Unauthorized("missing or invalid authorization header"))
            return
        }

        tokenString := strings.TrimPrefix(authHeader, "Bearer ")
        claims, err := tm.VerifyAccess(tokenString)
        if err != nil {
            httpx.Fail(c, httpx.Unauthorized("token is invalid or expired"))
            return
        }

        c.Set("user_id", claims.Subject)
        c.Set("session_id", claims.Session)
        c.Next()
    }
}
```

---

## 10. Database & Transactions

### Safe Context-Propagated Transactions

In this architecture, repositories execute queries via `r.db.Db(ctx)`. When a usecase calls `WithTransaction(ctx, ...)`, the active transaction is placed into the context under an unexported type-safe key:

```go
// Usecase defines consumer interface locally:
type txManager interface {
    WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

func (u *orderUsecase) PlaceOrder(ctx context.Context, in CreateOrderInput) (*model.Order, error) {
    var order *model.Order
    err := u.tx.WithTransaction(ctx, func(txCtx context.Context) error {
        // All repository calls using txCtx run inside the same BEGIN ... COMMIT
        if err := u.inventoryRepo.DeductStock(txCtx, in.ProductID, in.Quantity); err != nil {
            return err // Automatically rolls back
        }

        created, err := u.orderRepo.Create(txCtx, in.ToModel())
        if err != nil {
            return err // Automatically rolls back
        }

        order = created
        return nil // Automatically commits
    })
    return order, err
}
```

### Nested Transactions via Savepoints

If a function already inside a transaction calls `WithTransaction`, the client automatically creates a **savepoint** instead of a second transaction. A failure in the inner operation rolls back only its own work, keeping the outer transaction intact.

### Connection Pooling Configuration

Configure PostgreSQL pool limits in `.env`:

```env
POSTGRES_MAX_OPEN_CONNS=25
POSTGRES_MAX_IDLE_CONNS=10
POSTGRES_CONN_MAX_LIFETIME=15m
POSTGRES_CONN_MAX_IDLE_TIME=5m
POSTGRES_PREPARE_STMT=true       # Set to false behind transaction-mode PgBouncer
```

---

## 11. gRPC Service Development

### 1. Scaffold gRPC Support

Scaffold a feature with gRPC:

```bash
gen make:module inventory --grpc
```

Or add gRPC to an existing module:

```bash
gen make:grpc inventory
```

This creates:
- `modules/inventory/grpc/inventory.proto`
- `modules/inventory/grpc/server.go`

### 2. Compile Protobuf Files

```bash
make proto   # Or: gen proto:build
```

`gen proto:build` automatically uses `buf` (if `buf.gen.yaml` exists) or `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc`.

### 3. Implement the Server Stub (`modules/inventory/grpc/server.go`)

```go
package grpcsvc

import (
	"context"
	"github.com/MAbduhI/go-boilerplate/modules/inventory/usecase"
	pb "github.com/MAbduhI/go-boilerplate/modules/inventory/grpc"
)

type Server struct {
	pb.UnimplementedInventoryServiceServer
	usecase usecase.InventoryUsecase
}

func (s *Server) GetInventory(ctx context.Context, req *pb.GetInventoryRequest) (*pb.GetInventoryResponse, error) {
	out, err := s.usecase.GetByID(ctx, req.GetId())
	if err != nil {
		return nil, StatusError(err) // Translates httpx sentinels to gRPC status codes
	}
	return &pb.GetInventoryResponse{
		Item: &pb.InventoryItem{Id: out.ID, Name: out.Name},
	}, nil
}
```

---

## 12. Workspace Mechanics & Dependency Rules

### The Golden Rule: Sibling Modules Do Not `require` Each Other

In this workspace:
- `modules/example` imports `github.com/MAbduhI/go-boilerplate/shared/...`
- **However, `modules/example/go.mod` does NOT contain `require .../shared`!**

#### Why?
Go workspaces (`go.work`) resolve sibling directories from disk automatically. If you add an unpublished pseudo-version like `require shared v0.0.0`, Go tries to reach the network to resolve `shared/v0.0.0`, breaking offline builds and causing graph resolution failures.

### Dependency Management Best Practices

1. **Only run `make tidy`**:
   `make tidy` tidies `shared` and `cmd/gen`. **Never run `go mod tidy` inside `app/` or `modules/*`**.
2. **Adding a 3rd-party library to a module**:
   To add a package (e.g. `github.com/shopspring/decimal`) to `modules/stock`:
   ```bash
   cd modules/stock
   go get github.com/shopspring/decimal
   ```
3. **Adding a 3rd-party library to `shared`**:
   ```bash
   cd shared
   go get github.com/lib/pq
   ```

---

## 13. Testing, Linting & CI

### Running the Test Suite

```bash
make test                 # Run unit tests across every workspace module
make test-race            # Run tests with race detection (CGO_ENABLED=1)
make test-integration     # Run tests requiring live Docker services (make up first)
make cover                # Run tests with code coverage summary
```

### Formatting & Linting

```bash
make fmt                  # Format all Go code across the workspace
make fmt-check            # Verify formatting without modifying files
make lint                 # Run golangci-lint on every module
make lint-fix             # Run golangci-lint with auto-fix
make check                # Run full CI suite: fmt-check + vet + lint + test
```

### Writing Tests

#### Repository Unit Tests with `sqlmock`
Repository tests use `DATA-DOG/go-sqlmock` to verify exact SQL statements and transaction behavior without needing a live database.

#### Usecase Unit Tests with Fakes
Because usecases depend on repository interfaces and a narrow `txManager` interface, you can test complex business logic by passing lightweight mock implementations without GORM or database dependencies.

---

## 14. Graceful Shutdown & Production Deployment

### How Graceful Shutdown Works

When Kubernetes or Docker sends a `SIGTERM` or `SIGINT`:

```
SIGTERM signal received
  ↓
1. Stop accepting new HTTP connections
  ↓
2. Drain in-flight HTTP requests (SERVER_SHUTDOWN_TIMEOUT, default 15s)
  ↓
3. Execute registered close hooks in LIFO (reverse) order:
   - Close Redis / Kafka / Temporal
   - Flush Kafka producers
   - Drain & close PostgreSQL pool
  ↓
Process exits cleanly with code 0
```

Because resources are drained in reverse registration order, in-flight HTTP requests have full access to database handles while completing.

---

## 15. Troubleshooting & Common Pitfalls

### 1. `JWT_SECRET: required env JWT_SECRET is not set`
- **Cause**: Booting without configuring `.env`.
- **Fix**: Copy `.env.example` to `.env` and set `JWT_SECRET` with at least 32 characters.

### 2. `pattern ./...: directory prefix . does not contain modules listed in go.work`
- **Cause**: Running `go build ./...` or `go test ./...` from the workspace root.
- **Fix**: A Go workspace has no root module. Use `make build` or `make test`, which iterates over the modules listed in `go.work`.

### 3. `go mod tidy` breaks module builds
- **Cause**: Running `go mod tidy` inside `app/` or `modules/<feature>`, which adds an unresolvable `require .../shared v0.0.0`.
- **Fix**: Remove the `require .../shared` line from the module's `go.mod`, and run `make tidy` instead.

### 4. Soft-deleted records appearing in query results
- **Cause**: Forgetting the `postgres.NotDeleted` scope in a custom query.
- **Fix**: Add `.Scopes(postgres.NotDeleted)` to your GORM query in the repository:
  ```go
  r.db.Db(ctx).Model(&model.Product{}).Scopes(postgres.NotDeleted).Find(&out)
  ```

### 5. Queries escaping a transaction
- **Cause**: Holding a `*gorm.DB` struct field in the repository instead of calling `r.db.Db(ctx)`.
- **Fix**: Never store `*gorm.DB` in a repository struct. Always accept `postgres.DB` and call `r.db.Db(ctx)`.

---

## 16. Command Cheat Sheet

### Common Make Targets

```bash
make help               # Display all available make targets
make up                 # Start local Postgres, Redis, and Mailpit
make down               # Stop local Docker services
make run                # Run the API server locally
make check              # Run formatting, vet, lint, and tests
make test               # Run all unit tests
make lint               # Run golangci-lint across all modules
make fmt                # Format all Go files
make gen                # Recompile and install the gen CLI
make proto              # Compile all .proto files into Go code
make tools              # Install developer toolchain binaries
```

### Common `gen` Commands

```bash
gen make:module <name>               # Scaffold full CRUD feature module
gen make:module <name> --grpc        # Scaffold module with gRPC proto & stub
gen make:module <name> --base BaseModelUUID  # Scaffold module with UUIDv7 PK
gen make:usecase <name> <Method>     # Scaffold custom usecase method
gen make:connector <name>            # Scaffold shared connector
gen proto:build                      # Compile all module proto files
gen --help                           # View generator CLI help
```

