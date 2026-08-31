# Architecture

How this repository is put together and why. Read
[README.md](../README.md) first for the quick start and the command reference.

---

## Layers

Clean Architecture, one direction of dependency:

```
handler  →  usecase  →  repository  →  model
  (Gin)     (rules)      (GORM)       (structs)
```

| Layer | Knows about | Must not know about |
|---|---|---|
| `handler` | usecase, `httpx`, Gin | GORM, SQL |
| `usecase` | repository interface, `model`, `httpx` | GORM, Gin, `*gorm.DB` |
| `repository` | `model`, `postgres`, GORM | Gin, HTTP |
| `model` | nothing but the standard library | everything |

Two details make this hold up rather than being aspirational:

**The usecase declares its own transaction interface.** It needs one method, so
it defines it locally instead of importing the Postgres package:

```go
// modules/<entity>/usecase/<entity>.go
type txManager interface {
    WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
```

`*postgres.Client` satisfies it structurally. The usecase layer therefore has no
persistence import at all, and a test can substitute a fake without a database.
This is the consumer-defined-interface idiom: the interface belongs to the code
that uses it, not the code that implements it.

**Repositories translate driver errors at the boundary.** A repository turns
`gorm.ErrRecordNotFound` into `httpx.NotFound(...)`. Neither the usecase nor the
handler ever learns that GORM exists, and the handler's single error exit
(`httpx.Fail`) maps the sentinel to a status code.

### Domain models carry tags but no GORM types

`shared/database/model` imports no GORM package. Struct tags name GORM's column
options — that costs nothing at compile time — but every type is standard
library:

```go
type BaseModel struct {
    ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
    CreatedAt time.Time  `json:"created_at" gorm:"type:timestamptz;not null;autoCreateTime"`
    UpdatedAt time.Time  `json:"updated_at" gorm:"type:timestamptz;not null;autoUpdateTime"`
    DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"type:timestamptz;index"`
}
```

`DeletedAt` is a `*time.Time`, not a `gorm.DeletedAt`. **That has a consequence
worth knowing:** GORM's automatic soft-delete filtering only triggers on its own
type, so it does not apply here. Soft delete is explicit instead —
the repository stamps `deleted_at` on delete, and every read goes through a
scope:

```go
db.Scopes(postgres.NotDeleted).Find(&out)
```

The generated repository already does both. If you add a query by hand, add the
scope.

`BaseModelUUID` is the alternative, keyed by a time-ordered UUIDv7 so index
inserts stay local instead of scattering the way v4 does. Choose per entity with
`gen make:module <entity> --base BaseModelUUID`, or repo-wide via
`shared.base_model.type` in `.forge.yaml`.

---

## Why one Go module per feature

`modules/<entity>/repository` and `.../usecase` are unreachable from other
modules. Go cannot import across a module boundary without an explicit `require`,
so this is enforced by the build, not by review. A feature exposes exactly one
entry point — its handler's `Wire` — and everything behind it is private.

`shared/` never depends on `modules/*`. The arrow points one way, always.

```
app  →  modules/*  →  shared
                       ↑
              (nothing points back)
```

---

## Workspace mechanics

### Sibling modules carry no `require`

This is the part most likely to surprise you, and the reason the `go.mod` files
have a comment about it.

`modules/example` imports `shared`, but **`modules/example/go.mod` does not
require it.** That is deliberate and load-bearing.

A `require` needs a version. `shared` is unpublished, so the only candidate is a
placeholder like `v0.0.0` — and once you write that, the go command tries to
resolve it: it looks for the repository tag `shared/v0.0.0`, fails, and the build
breaks. Not immediately, which is what makes it confusing: it breaks as soon as
`shared` gains an external dependency of its own, because computing the pruned
module graph then requires reading `shared`'s `go.mod` *at that version*. The
error points at unrelated files:

```
shared/database/model/base_uuid.go:6:2: github.com/.../shared@v0.0.0:
    reading .../shared/go.mod at revision shared/v0.0.0: unknown revision
```

The usual workaround is a `replace` directive. It is not needed: omitting the
requirement entirely lets `go.work`'s `use` directive resolve the module from
disk, which is exactly what a workspace is for. So each `go.mod` lists **only its
external dependencies**, and the workspace supplies the rest.

Two consequences:

- **`go mod tidy` cannot run on `app` or `modules/*`.** It would try to resolve
  the sibling imports and add the bad `require` back. `make tidy` therefore
  tidies only `shared` and `cmd/gen`, which have no sibling imports.
- **These modules build only inside the workspace.** That is the correct
  trade-off for an unpublished monorepo. If you ever publish a module, tag it and
  add a real `require`.

The generator knows this rule: `resolveRequires` deliberately omits `shared`, and
copies third-party versions from `shared/go.mod` so every module in the workspace
agrees on one version of Gin and GORM — and so scaffolding works offline.

### Building the workspace

A Go workspace has no root module, so `go build ./...` from the top level matches
nothing:

```
pattern ./...: directory prefix . does not contain modules listed in go.work
```

Every Makefile target therefore loops over the modules parsed out of `go.work`,
which means `make build` keeps working as `gen` adds modules. To work on one
module, `cd` into it and use `go build ./...` normally.

---

## Transactions

`postgres.Client.Db(ctx)` returns the ambient transaction when `ctx` carries one
and the pool otherwise. Repositories always call it and never hold a `*gorm.DB`:

```go
func (r *stockRepository) GetByID(ctx context.Context, id uint) (*model.Stock, error) {
    var out model.Stock
    err := r.db.Db(ctx).Scopes(postgres.NotDeleted).First(&out, "id = ?", id).Error
    ...
}
```

`WithTransaction` puts the transaction into the context it passes to the
callback, so anything reached through `Db(ctx)` joins it:

```go
err := u.tx.WithTransaction(ctx, func(ctx context.Context) error {
    existing, err := u.repo.GetByID(ctx, in.ID)   // inside the transaction
    if err != nil {
        return err
    }
    return u.repo.Update(ctx, existing)           // same transaction
})
```

The context key is **one unexported typed key**, `txKey struct{}`, used by both
the writer and the reader — `shared/database/postgres/context.go` is the only
file allowed to touch that slot. An unexported empty struct type is the right
shape for a context key: unique per package by construction, so it cannot collide
with another package's key and cannot be forged from outside.

This is the fix for the inherited bug described in the README. The regression
tests in `transaction_test.go` use `sqlmock` and assert the exact statement
sequence, so a write that escapes the transaction fails on an out-of-order
expectation rather than passing quietly.

**Nested calls use savepoints.** If `ctx` is already in a transaction,
`WithTransaction` opens a savepoint instead of a second transaction. A usecase
can therefore be called standalone or from inside a larger transaction, and a
failure rolls back only its own work — `TestNestedTransactionRollbackKeepsOuterAlive`
covers it.

### Pool configuration

Every limit reaches the underlying `*sql.DB` and is env-driven —
`POSTGRES_MAX_OPEN_CONNS`, `POSTGRES_CONN_MAX_LIFETIME`, and so on. Durations are
`time.Duration` strings, so the unit is in the value. `Config.normalize` clamps
`MaxIdleConns` to `MaxOpenConns`, because `database/sql` would otherwise
reinterpret the contradiction silently.

Set `POSTGRES_PREPARE_STMT=false` behind a transaction-mode pooler such as
PgBouncer, which does not keep prepared statements across checkouts.

---

## Router wiring

`app/internal/router/router.go` owns two marker blocks. `gen make:module` adds
one line to each and nothing else; if a marker is absent it prints the lines and
edits nothing.

The inserted call is the module's own composition root:

```go
// modules/<entity>/handler/wire.go
func Wire(rg *gin.RouterGroup, db postgres.DB) *StockHandler {
    repo := repository.NewStockRepository(db)
    uc := usecase.NewStockUsecase(repo, db)
    h := NewHandler(uc)
    h.RegisterRoutes(rg)
    return h
}
```

**Why `Wire` rather than explicit DI in the router.** The alternative —
`stock.NewHandler(deps.StockUsecase).RegisterRoutes(v1)` — needs a
`StockUsecase` field on `Dependencies`, so generating a module would mean editing
two places and the result would not compile until you did. `Wire` keeps it to one
line per marker and the app builds immediately. The app also does not need to
know that a `StockRepository` exists, which is the point of the module boundary.

If you prefer explicit DI, add the field to `Dependencies`, construct the layers
in `main.go`, and replace the generated line — the marker block is ordinary code.

A module needing more than the database takes another parameter: add the field to
`Dependencies` and widen that module's `Wire`.

---

## Errors and responses

`shared/httpx` is the single vocabulary. Usecases and repositories return
sentinels (`httpx.ErrNotFound`, `httpx.ErrConflict`, …) or an `*httpx.Error`
carrying a client-safe message plus optional field details. Handlers have one
error exit:

```go
if err != nil {
    httpx.Fail(c, err)
    return
}
```

`Fail` resolves the status with `errors.Is`, so wrapping never loses the mapping.
Anything mapping to 5xx is reported to the client as a generic message while the
real cause goes to the log — `*httpx.Error` keeps the internal cause in an
unexported field that is never serialised.

Every response uses one envelope, so clients parse success and failure with one
type:

```json
{ "success": true,  "message": "ok", "data": {...}, "meta": {...} }
{ "success": false, "message": "not found", "error": { "code": "NOT_FOUND" } }
```

The same sentinels serve gRPC: `grpcsvc.StatusError` maps them to `codes.*`, so a
usecase never learns which transport called it.

### Validation

Per-field rules are Gin binding tags on the request struct, which lives in the
handler package because binding is a transport concern:

```go
type createRequest struct {
    Name string `json:"name" binding:"required,min=1,max=255"`
}
```

`shared/validation` exists only for what a tag cannot express — comparisons
between fields, conditional requirements — and it hooks *Gin's own* validator
rather than building a second one, so a registered rule is usable directly from a
binding tag. It also turns validator errors into per-field API detail keyed by
JSON field name.

Partial updates use pointer fields: `*string` distinguishes "key absent" from
"key sent as empty string".

---

## Adding a connector

Connectors are generated so a downstream project gets identical code:

```bash
gen make:connector redis
```

To add a new kind:

1. Write `cmd/gen/templates/connector/<name>.go.tmpl`. Follow the existing
   shape — a `Config` struct, a `LoadConfig()` reading `<NAME>_*` env vars, a
   `New(ctx, cfg)` that pings before returning, and a `Close`.
2. Register it in the `connectors` map in `cmd/gen/connector.go` with its
   destination directory and third-party modules.
3. `make gen && gen make:connector <name>`.

Conventions that keep connectors interchangeable:

- **Config from env, prefixed** by the connector name.
- **Durations are `time.Duration`**, never a bare int.
- **Ping in `New`.** Most clients dial lazily, so without a ping a bad host
  surfaces as a confusing error on the first request instead of a boot failure.
- **Translate driver sentinels** into the package's own (`ErrCacheMiss`,
  `nosql.ErrNotFound`) so callers do not import the driver to check an error.
- **`Close` returns `error`** and gets registered with the shutdown registry.

### The NoSQL contract

`mongo`, `cassandra` and `dynamodb` implement `nosql.NoSQLStore`. The interface is
deliberately narrow — the document operations all three share, and nothing else.
A backend-specific capability (a Mongo aggregation, a DynamoDB transactional
write) belongs on the concrete type, reached by type assertion, not on the
interface where two of three implementations would return `ErrUnsupported`.

Where a backend cannot express something, it says so instead of guessing:
Cassandra and DynamoDB both paginate by token, so `WithSkip` returns
`nosql.ErrUnsupported` rather than handing back page 1 when you asked for page 3.
Cassandra also refuses to add `ALLOW FILTERING` silently, since that turns a
query into a cluster-wide scan; opt in with `CASSANDRA_ALLOW_FILTERING=true`.

`shared/database/nosql/codec.go` converts between structs and
`map[string]any` for backends without their own codec, using `db` tags then
`json` tags, flattening embedded structs so a base model's columns sit alongside
the entity's own.

---

## Testing

`make test` runs every module; `make test-race` adds the detector.

What is covered and why:

| Area | Test | Pins |
|---|---|---|
| Transactions | `database/postgres/transaction_test.go` | Writes land inside `BEGIN`/`COMMIT`; rollback on error and on panic; savepoint nesting |
| Context key | `database/postgres/context_test.go` | Set-then-get round trip, and that a *string* key does not resolve — the original defect |
| Pool config | `database/postgres/config_test.go` | Every knob reachable from env; idle clamped to open; DSN quoting |
| Redis TTL | `cache/redis/redis_test.go` | `REDIS_DEFAULT_TTL` parsing; the three meanings of `ttlOverride` |
| Env loader | `config/env_test.go` | Quoting and escapes; the real environment wins over `.env`; unitless durations rejected |
| Errors | `httpx/error_test.go` | Status mapping survives wrapping; 5xx causes are not leaked |
| Security | `security/security_test.go` | bcrypt's 72-byte limit; expired tokens; `alg:none` rejected; access token refused at refresh |
| Shutdown | `shutdown/shutdown_test.go` | LIFO order; one failure does not strand the rest; idempotent |
| NoSQL codec | `database/nosql/codec_test.go` | Tag precedence, embedded flattening, nil pointers omitted |
| Generator | `cmd/gen/naming_test.go` | Naming convention and pluralisation, including irregulars |
| Mailpit | `mail/mailpit/mailpit_integration_test.go` | Real SMTP delivery, read back through Mailpit's API (tagged `integration`) |

The Postgres tests use `sqlmock` rather than a live database, so they assert the
exact statement sequence and run in milliseconds. Connector tests cover
configuration and pure logic; anything needing a live service belongs behind the
`integration` build tag, which `.golangci.yml` already knows about. The Mailpit
test is the worked example:

```bash
make up
make test-integration        # or: cd shared && go test -tags integration ./mail/mailpit/
```

---

## Linting

`.golangci.yml` (schema v2) applies to every module via `make lint`. The
selection favours linters that find defects — unchecked errors, leaked bodies,
dropped contexts, `errors.Is` misuse — over stylistic ones, so a clean run is
meaningful. All four modules currently report zero issues.

Deliberate exemptions, each for a stated reason:

- **`gocritic: hugeParam`** — config structs are passed by value on purpose:
  copied once at startup, immutable at the call site. The copy is irrelevant next
  to opening a connection.
- **`govet: shadow`** — `if err := f(); err != nil` shadows by design; flagging
  it fights the language.
- **`gosec` in `cmd/gen/`** — a code generator's whole job is file I/O on
  operator-supplied paths and shelling out to `go` and `protoc`. It also writes
  *source files*, which must be world-readable; gosec's `0600` expectation is for
  secrets.
- **`staticcheck ST1003` on `Client.Db`** — the name is kept from the project
  this was ported from so existing repositories read the same. `DB` would collide
  with the `DB` interface the type implements.

---

## gRPC

`gen make:grpc <entity>` writes a `.proto` skeleton and a Go server stub.
Compiling is a separate step (`gen proto:build`) because it needs `protoc` or
`buf` plus plugins on `PATH`; a scaffolding command that failed for want of an
external toolchain would be useless offline. When the toolchain is missing,
`proto:build` prints install instructions and exits non-zero rather than
generating nothing quietly.

`buf` wins if `buf.gen.yaml` exists at the root; otherwise `protoc` runs per
file, generating beside the `.proto` with `paths=source_relative`.

The stub compiles before the `.pb.go` files exist — it holds the usecase and the
error mapping but does not yet name the generated types — so a module scaffolded
with `--grpc` builds immediately. Once `proto:build` has run, embed the generated
`Unimplemented<Entity>ServiceServer`, uncomment `Register`, and implement the
RPCs as thin translations, exactly like the REST handler.

`*.pb.go` is gitignored: the `.proto` is the source of truth. Commit the
generated files instead if your CI has no protoc.
