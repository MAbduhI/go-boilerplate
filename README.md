# go-boilerplate

A reusable Go backend boilerplate: **Gin + GORM + PostgreSQL**, Clean
Architecture, laid out as a **multi-module monorepo** driven by `go.work`, with a
code generator (`gen`) that scaffolds every layer so new features and new
projects start from the same shape.

Ported and refactored from
[stock-management-rest-api](https://github.com/MAbduhI/stock-management-rest-api)
— Echo replaced with Gin, the single module split into a workspace, and the
known defects in that codebase fixed rather than carried over (see
[What was fixed in the port](#what-was-fixed-in-the-port)).

---

## Quick start

```bash
# 1. Development toolchain (golangci-lint, protoc plugins, the gen CLI)
make tools

# 2. Local services — Postgres, Redis and Mailpit
make up

# 3. Configuration
cp .env.example .env
#    then set JWT_SECRET; it has no default and the app will refuse to boot

# 4. Run
make run
```

Verify it is alive:

```bash
curl localhost:8080/healthz              # process is up
curl localhost:8080/readyz               # ...and the database answers
curl localhost:8080/api/v1/examples      # the generated example module
```

Scaffold a feature:

```bash
gen make:module stock
```

That writes the model, repository, usecase and handler, registers the route in
the central router, and adds the module to `go.work`. `GET /api/v1/stocks` works
immediately — add real fields to `modules/stock/model/stock.go` and go.

---

## Project structure & repository layout

```
go-boilerplate/
├── go.work                      # defines the multi-module workspace; committed
├── .forge.yaml                  # generator config: module prefix, base model, paths
├── .golangci.yml                # lint rules, applied to every module
├── docker-compose.yml           # local dev services (profiled)
├── Makefile                     # build/test/lint across all modules
├── README.md                    # project overview & quick start
├── HOW_TO_USE.md                # comprehensive developer guide & reference manual
│
├── cmd/gen/                     # the code generator (its own module)
│   ├── main.go                  # CLI, commands, flags
│   ├── forge.go  naming.go      # config loading, name inflection
│   ├── render.go gomod.go       # template rendering, go.mod handling
│   ├── router.go gowork.go      # marker insertion, `go work use`
│   └── templates/               # embedded: module/, grpc/, connector/
│
├── shared/                      # generic building blocks — ONE module
│   ├── config/                  # .env loader + typed env getters
│   ├── logger/                  # structured logging over log/slog
│   ├── httpx/                   # Gin response envelope + error mapping
│   ├── validation/              # cross-field rules on Gin's validator
│   ├── security/                # bcrypt hashing, JWT issue/verify
│   ├── shutdown/               # LIFO close registry
│   ├── database/
│   │   ├── model/               # BaseModel, BaseModelUUID
│   │   ├── postgres/            # GORM pool + transaction-in-context
│   │   └── nosql/               # NoSQLStore contract + codec
│   │       ├── mongo/  cassandra/  dynamodb/
│   ├── cache/redis/
│   ├── search/elasticsearch/
│   ├── mail/mailpit/
│   └── messaging/kafka/  rabbitmq/  temporal/
│
├── modules/                     # feature modules — one Go module EACH
│   └── example/
│       ├── go.mod
│       ├── model/               # domain struct
│       ├── repository/          # persistence (private to this module)
│       ├── usecase/             # business rules (private to this module)
│       ├── handler/             # Gin REST + Wire composition root
│       └── grpc/                # optional: .proto + server stub
│
└── app/
    ├── cmd/api/main.go          # wiring + graceful shutdown
    └── internal/
        ├── router/router.go     # central router with the generator markers
        └── middleware/          # request ID, access log, recovery, CORS
```

Full rationale for the layout, the dependency rules and the workspace mechanics:
**[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** and **[HOW_TO_USE.md](HOW_TO_USE.md)**.

### Why a module per feature

`modules/<entity>/repository` and `.../usecase` are unreachable from any other
module. That is not a naming convention or a lint rule — Go cannot import across
a module boundary without an explicit `require`, so the encapsulation is
structural. A feature is reachable only through its handler, and `shared/` never
depends on `modules/*`, so the dependency arrow points one way only.

---

## The `gen` generator

Install with `make gen` (or `go install ./cmd/gen`). Templates are embedded in
the binary, so it works from anywhere.

| Command | What it does |
|---|---|
| `gen make:module <entity>` | model + repository + usecase + handler, registers the route, runs `go work use` |
| `gen make:module <entity> --grpc` | as above, plus the gRPC skeleton |
| `gen make:model <entity>` | model only |
| `gen make:handler <entity>` | REST handler only |
| `gen make:usecase <entity>` | generic CRUD usecase |
| `gen make:usecase <entity> <Method>` | one custom method, in its own file |
| `gen make:repository <entity>` | repository only |
| `gen make:grpc <entity>` | `.proto` skeleton + Go server stub |
| `gen proto:build` | compiles every `modules/*/grpc/*.proto` |
| `gen make:connector <name>` | scaffolds a `shared/` connector |

Flags: `--grpc`, `--base <BaseModel\|BaseModelUUID>`, `--force`, `--dry-run`.

Connector names: `redis`, `elasticsearch`, `mongo`, `cassandra`, `dynamodb`,
`kafka`, `temporal`, `rabbitmq`, `mailpit`.

### Naming convention

Fixed, so it is never re-litigated per generate. `gen make:module user-profile`,
`user_profile` and `UserProfile` all produce:

```
modules/userprofile/
├── model/user_profile.go        →  type UserProfile
├── repository/user_profile.go   →  UserProfileRepository  + userProfileRepository
├── usecase/user_profile.go      →  UserProfileUsecase     + userProfileUsecase
└── handler/user_profile.go      →  type UserProfileHandler
```

Table `user_profiles`, routes under `/api/v1/user-profiles`. Initialisms are kept
upper-case (`api_key` → `APIKey`), and pluralisation handles the irregular cases
(`category` → `categories`, `person` → `people`).

### Router wiring

`gen make:module` inserts exactly one line into each marker block and touches
nothing else. If a marker is missing it prints the lines and edits nothing —
guessing an insertion point is how a generator corrupts a router.

```go
import (
    // forge:imports:start — managed by `gen`, do not edit between the markers
    stock "github.com/MAbduhI/go-boilerplate/modules/stock/handler"
    // forge:imports:end
)

func RegisterRoutes(r *gin.Engine, deps *Dependencies) {
    v1 := r.Group("/api/v1")
    // forge:routes:start — managed by `gen`, do not edit between the markers
    stock.Wire(v1, deps.DB)
    // forge:routes:end
}
```

`Wire` is the module's own composition root: it builds repository → usecase →
handler internally. That is what keeps the generated change to one line per
marker and makes the result compile with no follow-up edit. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#router-wiring) for the explicit-DI
alternative.

Re-running `gen make:module` is safe: existing files are skipped unless
`--force`, and marker lines are de-duplicated.

---

## Configuration

Everything is environment-driven, documented in
**[.env.example](.env.example)**. `shared/config` loads `.env` without a
dependency and **never overrides a variable already set in the real
environment** — in a container the environment wins, so a stray committed `.env`
cannot shadow it.

Every timeout and TTL is a `time.Duration` string (`15m`, `500ms`, `1h30m`).
A bare number is rejected and the default applies, so seconds-versus-milliseconds
is never guesswork.

Two values have no safe default and fail at boot instead:

- `JWT_SECRET` — minimum 32 bytes. The reference project fell back to
  `"your-secret-key"`.
- Postgres credentials, if the database is unreachable — `postgres.New` pings
  before returning, so a misconfiguration is a boot failure, not a mystery 500
  on the first request.

---

## Connectors

`shared/` holds the infrastructure adapters. Each was a deliberate build-vs-fetch
decision:

| Component | Decision | Note |
|---|---|---|
| `config`, `logger`, `httpx`, `validation` | **built** | No dependency needed; `logger` wraps `log/slog`. |
| `database/postgres` | **built wrapper** | GORM + pgx. Pool fully configurable; transaction-in-context fixed. |
| `mail/mailpit` | **built** | Mailpit is plain SMTP, so `net/smtp` suffices. |
| `messaging/rabbitmq` | **contract only** | Interfaces, no implementation — bind your existing connector. |
| `cache/redis` | fetched | `redis/go-redis/v9`. `REDIS_DEFAULT_TTL` + per-call override. |
| `database/nosql/mongo` | fetched | Official driver; the wire protocol is not worth rebuilding. |
| `database/nosql/cassandra` | fetched | `gocql`. |
| `database/nosql/dynamodb` | fetched | AWS SDK v2 — never hand-roll SigV4. |
| `search/elasticsearch` | fetched | Official v8 client. |
| `messaging/kafka` | fetched | `segmentio/kafka-go`, pure Go, no CGO. |
| `messaging/temporal` | fetched | `go.temporal.io/sdk` + worker starter. |

The three NoSQL backends implement one interface, `nosql.NoSQLStore`, so a
usecase depends on the contract rather than a driver. Where a backend genuinely
cannot express an operation — offset pagination on Cassandra or DynamoDB, both of
which paginate by token — it returns `nosql.ErrUnsupported` rather than silently
returning the wrong page.

All connectors are generated from templates, so a downstream project gets the
same code:

```bash
gen make:connector redis
```

An existing connector directory is never overwritten; the command warns and stops
unless you pass `--force`.

---

## Graceful shutdown

`app/cmd/api/main.go` traps `SIGINT`/`SIGTERM`, drains HTTP first, then closes
every open resource in reverse registration order via `shared/shutdown`:

```
signal → stop accepting → drain in-flight requests → close resources (LIFO) → exit
```

Draining HTTP before the connectors is the ordering that matters: a request still
running needs its database handle. A failing hook does not stop the others — on
shutdown the goal is to release as much as possible — and every failure is
reported, joined.

Only Postgres is wired on by default. Every other connector is present in
`openConnectors` as a complete, commented block next to its `registry.Register`
call, so switching a connector on and closing it cleanly are the same edit.

---

## Development

```bash
make help          # list every target
make build         # build all modules
make test          # test all modules
make test-race     # with the race detector
make lint          # golangci-lint, all modules
make fmt           # gofmt -w
make check         # fmt-check + vet + lint + test (what CI runs)
make up / down     # dev services
make proto         # compile .proto files
```

`make build` loops over the modules in `go.work` rather than running
`go build ./...` from the root. A Go workspace has no root module, so `./...`
from the top level matches nothing — see
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#building-the-workspace).

### Local services

`make up` starts Postgres, Redis and Mailpit. The heavier services are behind
profiles so a laptop is not asked to run Cassandra and Elasticsearch to edit a
handler:

```bash
docker compose --profile nosql  up -d   # mongo, cassandra, dynamodb
docker compose --profile search up -d   # elasticsearch
docker compose --profile queue  up -d   # kafka, temporal (+ UI on :8088)
docker compose --profile all    up -d
```

Mail sent through the Mailpit connector is readable at
<http://localhost:8025>.

---

## What was fixed in the port

The reference project's defects were repaired during the port, not copied.

**Transaction context key mismatch — silent data-integrity bug.**
`WithTransaction` stored the transaction under a private `ctxKey{}` struct but
`Db(ctx)` read it back with the string literal `"txContext"`. Those are different
keys, so the lookup *always* missed: every repository call inside a transaction
ran on a separate pooled connection, outside `BEGIN`/`COMMIT`. Writes committed
independently and a rollback silently left earlier writes in place. There is now
one unexported typed key used by both sides, and
`shared/database/postgres/transaction_test.go` pins it with `sqlmock`, asserting
the statement order so a write that escaped the transaction fails the test.
Nested calls now open a savepoint rather than a second transaction, so a usecase
stays composable.

**Graceful shutdown was absent.** Nothing was closed on exit. Replaced with the
`shared/shutdown` registry and the drain-then-close sequence above.

**Domain models were coupled to GORM.** `shared/database/model` imports no GORM
package. Tags name GORM columns — free at compile time — but the types are
standard library, so `gorm.DeletedAt` is gone. Soft delete is therefore explicit:
the repository stamps `deleted_at` and reads filter through the
`postgres.NotDeleted` scope.

**Logging was unstructured.** `log.Printf` with a string level, unparseable
downstream. Replaced with `log/slog` — no new dependency — plus a request-scoped
logger carried in the context so a repository can log with the request ID without
threading it through every signature.

Three further defects surfaced while porting:

- **`VerifyRefresh` was a copy of `VerifyToken`** and never checked the token
  type, so any access token was accepted at the refresh endpoint and could be
  traded for a fresh long-lived pair. Tokens now carry a `typ` claim that is
  verified; `TestAccessTokenRejectedAtRefresh` covers it.
- **Errors were compared with `==`**, so a status mapping was lost the moment an
  error was wrapped. `httpx` uses `errors.Is`/`errors.As` throughout.
- **bcrypt cost was hardcoded at 14**, roughly a second per hash — an easy
  CPU-exhaustion target on a login endpoint. Now `BCRYPT_COST`, default 12.

---

## Documentation

- **[HOW_TO_USE.md](HOW_TO_USE.md)** — comprehensive developer guide, scaffolding walkthrough, connectors, transactions, and cheatsheet.
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — layer boundaries, workspace
  mechanics, why sibling modules carry no `require`, adding a connector, testing
  strategy.
- **[.env.example](.env.example)** — every environment variable, annotated.
- `gen --help` — generator reference.
