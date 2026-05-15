# HW4 — Before/After per decision

---

## 1. `IsNewTag` on the domain entity — *Information Expert*

The scanner owned a rule that depends on a field it doesn't own (`LastSeenTag`).

```go
// Before — internal/scanner/scanner.go
if rel.TagName != "" && rel.TagName != sub.LastSeenTag { /* notify */ }

// After — internal/domain/models.go
func (s *Subscription) IsNewTag(tag string) bool {
    return tag != "" && tag != s.LastSeenTag
}
```

Future rules ("ignore pre-releases", "skip drafts") now attach to the data owner, not the caller.

---

## 2. Atomic `CreateSubscriptionWithToken` — *High Cohesion + bugfix*

Two separate writes could split-fail, leaving a confirmable subscription with no token (user couldn't confirm and couldn't re-subscribe — unique index blocked them).

```go
// Before — service.Subscribe
s.repo.CreateSubscription(ctx, sub)   // commit 1
s.repo.CreateToken(ctx, token)        // commit 2 — can fail

// After — internal/repository/repository.go
func (r *Repository) CreateSubscriptionWithToken(ctx, sub, token) error {
    return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        if err := tx.Create(sub).Error; err != nil { return err }
        token.SubscriptionID = sub.ID
        return tx.Create(token).Error
    })
}
```

---

## 3. `SubscriptionRepo` + `TokenRepo` split — *ISP*

One 9-method interface bundled two unrelated concerns; every consumer depended on the whole surface.

```go
// Before — one fat interface
type SubscriptionRepository interface {
    CreateSubscription, FindByEmailAndRepo, FindByEmail, FindByUnsubscribeToken,
    Confirm, Delete, CreateToken, FindTokenByValue, DeleteToken
}

// After — two narrow ones, composed
type SubscriptionRepo interface { CreateSubscriptionWithToken, FindByEmailAndRepo, ... }
type TokenRepo        interface { FindTokenByValue, DeleteToken }
type SubscriptionRepository interface { SubscriptionRepo; TokenRepo }

// Service holds two narrow fields:
type Service struct { subs SubscriptionRepo; tokens TokenRepo; ... }
```

GORM `*Repository` satisfies both structurally — no impl change.

---

## 4. `TokenGenerator` injectable — *DIP + OCP*

Service depended on a concrete random source; tests had to monkey-patch `crypto/rand`.

```go
// Before
func (s *Service) generateToken() (string, error) {
    b := make([]byte, 32); rand.Read(b); return hex.EncodeToString(b), nil
}

// After
type TokenGenerator func() (string, error)
func RandomToken() (string, error) { u, _ := uuid.NewRandom(); return u.String(), nil }

type Service struct { ...; newToken TokenGenerator }
```

Swapping `crypto/rand`-hex → UUID v4 was a one-line change inside `RandomToken`.

---

## 5. `errorView` registry — *OCP + Pure Fabrication*

The same error→status switch was duplicated across 3 handlers and drifted (`ErrRateLimited` mapped in `Subscribe`, missing in `Confirm`).

```go
// Before — repeated in every handler
switch {
case errors.Is(err, domain.ErrInvalidRepoFormat): c.JSON(400, ...)
case errors.Is(err, domain.ErrRepoNotFound):      c.JSON(404, ...)
case errors.Is(err, domain.ErrAlreadySubscribed): c.JSON(409, ...)
// ...
}

// After — internal/api/errors.go
type httpView struct { status int; message string }
var errorView = map[error]httpView{
    domain.ErrInvalidEmail:      {400, "invalid email format"},
    domain.ErrRepoNotFound:      {404, "repository not found on GitHub or is private"},
    domain.ErrAlreadySubscribed: {409, "email already subscribed to this repository"},
    domain.ErrRateLimited:       {503, "service temporarily unavailable, try again later"},
    // ...
}
func writeError(c *gin.Context, op string, err error) {
    for sentinel, v := range errorView {
        if errors.Is(err, sentinel) {
            c.JSON(v.status, domain.ErrorResponse{Error: v.message})
            return
        }
    }
    log.Printf("%s error: %v", op, err)
    c.JSON(500, domain.ErrorResponse{Error: "internal server error"})
}
```

One table, one chain walk, one source of truth for both the status and the public copy. Unmapped errors are logged with `op` context and return a generic 500 — internal text never leaks.

---

## 6. `ctx` through every notifier signature — *LSP + Protected Variations*

`Send*(...) error` made any future async/outbox impl silently break the contract — no cancellation, no deadlines.

```go
// Before
SendConfirmation(email, repo, token, unsub string) error

// After
SendConfirmation(ctx context.Context, email, repo, token, unsub string) error
// + fast-fail on ctx.Err() before opening SMTP
```

Substitution is now honest. (Mid-send cancellation needs `smtp.Dial` rewrite — deferred.)

---

## 7. Scanner `WorkerPool` + tick budget — *SRP + Pure Fabrication + bugfix*

Sequential fan-out, no per-call deadline, no tick-budget protection. One slow GitHub call stalled the whole tick.

```go
// Before
for _, repo := range repos { s.checkRepo(ctx, repo) }   // sequential, no timeout

// After — internal/scanner/workerpool.go (new)
type WorkerPool struct{ size int }
func (p *WorkerPool) Run(ctx, jobs, handler) { /* bounded fan-out */ }

// scanner.go — per-call ctx + tick-budget skip
ctx, cancel := context.WithTimeout(parent, s.cfg.GitHubTimeout)
if elapsed := time.Since(start); elapsed > budget {
    select { case <-ticker.C: default: }   // drain pending tick
}
```

Cooperative rate-limit abort via `atomic.Bool` (was: shared cancellable ctx that killed siblings' deadlines).

---

## 8. `scanner.Config` replaces bare `interval` — *OCP*

Adding `SCAN_CONCURRENCY` would have required edits in 3 places.

```go
// Before
func New(repo, github, notifier, interval time.Duration) *Scanner

// After
type Config struct { Interval, Concurrency, GitHubTimeout }
func New(repo, github, notifier, cfg *Config) *Scanner
```

---

## 9. Composed `config.Config` — *Information Expert + Low Coupling*

18 flat fields; `main.go` hand-translated them into each adapter's `Config`.

```go
// Before
type Config struct {
    PostgresHost, PostgresPort, PostgresUser, PostgresPassword, PostgresDB,
    SMTPHost, SMTPPort, ..., RedisURL, GitHubToken, ScanInterval string
}

// After
type Config struct {
    DB DBConfig; Redis RedisConfig; SMTP notifier.Config
    Scanner scanner.Config; GitHub github.Config
    // flat singletons stay flat: Port, APIKey
}
```

Each sub-config lives in the package that owns the fields. Adding a knob is a one-place change.

---

## 10. `github.Config` in owning package — *Information Expert*

Timeout was hardcoded; operator had no knob.

```go
// Before
NewClient(token string) // http.Client{Timeout: 10 * time.Second}

// After — internal/github/client.go
type Config struct { Token string; Timeout time.Duration }
NewClient(cfg *Config) // http.Client{Timeout: cfg.Timeout}
```

---

## 11. `cache.Config.DSN()` — *Information Expert*

Only URL mode was supported; split host/port/password/db wasn't.

```go
// Before
NewRedis(url string)

// After — internal/cache/redis.go
type Config struct { URL, Host, Port, Password, DB string }
func (c *Config) DSN() string  // URL wins; else assemble; empty Host = no Redis
```

`cfg.Redis.DSN() == ""` is the canonical "no Redis" check in `main.go`.

---

## 12. Notifier split into `Composer` + `Mailer` — *SRP + DIP*

The single `Notifier` type fused three responsibilities: feature-specific composition (confirmation vs. release URLs, subjects, bodies), MIME envelope assembly, and SMTP transport. `SendConfirmation` and `SendReleaseNotification` were ~90% structurally identical, and `send()` mixed pure formatting with `smtp.SendMail` — untestable without a real SMTP server.

```go
// Before — one type doing composition + MIME + transport
func (n *Notifier) SendConfirmation(ctx, email, repo, token, unsub string) error {
    // build URLs, subject, plain, render HTML, then:
    return n.send(email, subject, plain, html, unsubscribeURL)
}
func (n *Notifier) send(to, subject, plain, html, unsubURL string) error {
    // 20+ lines of multipart MIME + smtp.SendMail in one breath
}

// After — three files, three responsibilities
// internal/notifier/composer.go
type Composer struct{ baseURL string }
func (c *Composer) Confirmation(email, repo, token, unsub string) (Message, error)
func (c *Composer) Release(email, repo, tag, unsub string) (Message, error)

// internal/notifier/mailer.go
type Message struct { To, Subject, PlainBody, HTMLBody string; Headers map[string]string }
type Mailer interface { Send(ctx context.Context, msg Message) error }
type SMTPMailer struct{ /* host, port, username, password */ }
func buildMIME(from string, msg Message) []byte  // pure, no I/O

// internal/notifier/notifier.go — slim orchestrator
type Notifier struct { composer *Composer; mailer Mailer }
func (n *Notifier) SendConfirmation(ctx, email, repo, token, unsub string) error {
    msg, err := n.composer.Confirmation(email, repo, token, unsub)
    if err != nil { return err }
    return n.mailer.Send(ctx, msg)
}
```

Public surface (`SendConfirmation`, `SendReleaseNotification`) is unchanged — `service` and `scanner` interfaces untouched. Adding a third email type = one composer method + one template. `buildMIME` is pure and unit-testable; `Mailer` is an interface, so future tests can inject a fake without standing up SMTP. The dead `n.cfg.Host == ""` runtime check was dropped — `config.validate()` already owns that boundary.

---

## 13. No `SCANNER_ENABLED` toggle — *YAGNI*

A boot-time flag for "API without the background scanner" had no consumer — no environment used it, and the alternative ways to achieve the same effect (stop the binary, scale the worker replica to zero) are operational, not config concerns.

```go
// cmd/server/main.go
go scan.Run(ctx)   // scanner always starts; no config bool gates it
```

The `Config` struct, env loader, and `getEnvBool` helper that would have backed the flag don't exist. The rule: no config knob without a concrete consumer asking for it.

---

## What was deliberately *not* changed

`service.Service` 4-way split, `Leader`/`Ticker`/`RepoHandler` interfaces, `ReleasePublisher` indirection, advisory-lock leader, `RateLimited` decorator. Each has one impl today and no concrete second consumer — speculation, not architecture. Full rationale in [`hw4-solid-grasp-summary.md`](./hw4-solid-grasp-summary.md) § "Deliberately deferred".

**Rule throughout: no abstraction without a concrete consumer.**
