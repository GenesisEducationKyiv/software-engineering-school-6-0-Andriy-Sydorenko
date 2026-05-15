# ADR-005: Token Strategy for Confirm and Unsubscribe Links

## Status
Accepted

---

## Context

Two unauthenticated link types are emailed to users:

1. **Confirmation link** (`GET /api/confirm/:token`) - completes double opt-in. One-shot.
2. **Unsubscribe link** (`GET /POST /api/unsubscribe/:token`) - soft-deletes a subscription. Long-lived; lives in mail clients indefinitely.

Both must be unforgeable by anyone who does not possess the token, revocable on the server, and cheap to validate. Two designs were available:

- **Opaque random tokens.** Server-generated random bytes, persisted, looked up on each request.
- **Signed tokens (HMAC / JWT).** Stateless tokens carrying the subscription ID and intent, validated by signature.

---

## Decision

Use **random (v4) UUIDs** as opaque tokens - generated via `github.com/google/uuid`'s `NewRandom`, formatted as the canonical 36-character string.

- **Two tokens per subscription, with different lifetimes:**
  - `confirmation_token` - separate table row (`confirmation_tokens`), one-shot, deleted on use.
  - `unsubscribe_token` - column on the `subscriptions` row, long-lived, revoked by soft-delete on the row.
- **Lookup is a unique-indexed DB read.** UUID v4 carries 122 bits of randomness (2^122 search space) - unguessable in practice; a timing leak via DB equality is not practically exploitable.
- **No secret rotation needed.** Tokens are not derived from a server secret; revoking a single token is a row-level operation.

---

## Consequences

**Positive**
- **Revocable per-token.** Soft-delete the subscription, the unsubscribe token is dead. Use the confirmation token, the row is gone. No "deny list" or revocation registry needed.
- **Tiny attack surface.** 122 bits of entropy = 2^122 search space. No structure for an attacker to attack - no claims, no signature, no key.
- **No key management.** No HMAC secret to rotate, leak, or version.
- **Independent lifecycles.** Confirm tokens expire; unsubscribe tokens don't. Modeling this with signed tokens would require either two signing keys or claim-based logic the verifier has to trust.

**Negative**
- **One DB lookup per validation.** Trivial cost (indexed unique lookup). Not stateless - but the system is already DB-bound.
- **Storage.** One row per pending confirmation, one column per subscription. Negligible.
- **Mail-client URL length.** UUID = 36 characters; well within any client's limit.

---

## Alternatives Considered

- **Signed JWT.** Rejected: revocation requires a deny list (defeats statelessness), key rotation adds operational complexity, and the only "benefit" - stateless verification - is irrelevant to a service that already does a DB write on confirm and a DB write on unsubscribe.
- **HMAC of `(subscription_id, intent, expiry)`.** Rejected for the same reason: revocation needs a deny list. Also: a leaked HMAC secret invalidates *every* outstanding link, including unsubscribe URLs sitting in mail clients - a very expensive recovery.
- **Single token used for both confirm and unsubscribe.** Rejected: confirm should be one-shot and short-lived; unsubscribe must survive in mail clients indefinitely. Mixing the two yields either a one-shot unsubscribe link (broken) or a permanent confirm link (a security risk).
- **Hand-rolled `crypto/rand` (32 bytes, hex).** Was the original implementation; replaced by UUID v4 to standardize on a well-known opaque-identifier format with library-level guarantees. UUID's 122 bits of entropy are still unguessable; the extra 134 bits of `crypto/rand` had no operational benefit.
- **Adding HMAC on top of opaque tokens.** Rejected: the opaque random token already serves as the unforgeable proof. HMAC adds no security here and introduces a signing key that must be managed and rotated.

---

## Operational Impact

- **Logs.** Tokens never logged.

---

## Security Considerations

- **Entropy source.** `github.com/google/uuid` reads from `crypto/rand` for v4 generation.
- **Email-bombing abuse.** A separate concern handled by rate-limiting `POST /api/subscribe` (system-design §11), not by token design.

---

## Future Work

- **Confirmation-token TTL + sweeper.** A `CONFIRM_TOKEN_TTL` (e.g. 24 h) plus a periodic job that hard-deletes expired tokens and their unconfirmed subscriptions, to bound abuse-driven row growth. Today tokens persist until use or unsubscribe.
- **Constant-time compare.** `subtle.ConstantTimeCompare` on the lookup path. Not strictly needed at 122 bits of entropy, but cheap to add as a defense-in-depth habit.
- **Referrer-Policy header** on the confirmation/unsubscribe HTML pages so tokens don't leak via outbound link clicks.
- **Metrics.** `confirm_attempts_total{outcome}`, `unsubscribe_attempts_total{outcome}` once a Prometheus endpoint is wired up.

---

## References

- ADR-001 - Primary Datastore (token storage)
- System Design §11 (security)
- `subtle.ConstantTimeCompare` - https://pkg.go.dev/crypto/subtle
- RFC 8058 - One-Click Unsubscribe (constrains the unsubscribe-token URL contract)
