# App Error Envelope Canonical Contract

Status: Draft v1  
Owner: Platform/Controlplane team  
Scope: Cross-module error envelope for controlplane services  
Upstream Idea: `controlplane/docs/idea/app-error-envelope-full-idea.md`

## 0) Contract Governance

### Contract Item
- `APPERR-GOV-001` Canonical ownership and change policy.

### Owner
- Platform/Controlplane (shared `pkg` layer), module owners (`internal/*/errorx`) as domain-kind owners.

### Rules
- Shared envelope lives in `pkg` and is reusable by all modules.
- Module-local `errorx` remains source-of-truth for domain/business class (`Kind`).
- Any change to `Kind/Reason/Cause` semantics MUST update this contract in the same PR.

### Invariants
- `Kind` is domain class, not infra/driver message.
- `Reason` is stable machine code, not raw dynamic text.
- `Cause` is internal only; never returned directly to client.

### Failure Semantics
- Violation is treated as contract drift and must block rollout.

### Verification Evidence
- Code review checklist + handler/service tests confirming `errors.Is` mapping and reason extraction.

---

## 1) Database Contract

### Contract Item
- `APPERR-DB-001` Error envelope does not introduce persistence schema as canonical path.

### Owner
- Platform/Controlplane.

### Rules
- No mandatory DB table/migration is required for v1 envelope adoption.
- If a module persists audit/error snapshots, persisted fields MUST be normalized (`kind`, `reason`, optional redacted `cause_class`) and must not store secrets.

### Invariants
- No raw SQL/driver error text is used as canonical reason label.
- DB write path for business data remains unchanged by envelope contract.

### Failure Semantics
- Persisting raw sensitive error details is a security violation.

### Verification Evidence
- Migration diff = none for v1; audit-log schema review if module opts in.

---

## 2) API Contract

### Contract Item
- `APPERR-API-001` Public error response remains generic and safe.

### Owner
- Transport/Handler per module.

### Rules
- Handler maps HTTP status by `errors.Is(err, Kind)`.
- Response body follows module API contract (generic message); must not include raw `Cause`.
- `Reason` may be exposed only if explicitly allowed by module security policy; default is internal-only.

### Invariants
- External clients must not infer internal topology/SQL details from error payload.
- Existing endpoint business semantics stay stable after envelope adoption.

### Failure Semantics
- Unknown/unmapped kind -> fallback internal error mapping without leaking details.

### Verification Evidence
- Handler transport tests for status mapping and response redaction.

---

## 3) Event/Job Contract

### Contract Item
- `APPERR-EVT-001` Error envelope propagation across async boundaries.

### Owner
- Producer/consumer module owners.

### Rules
- If error metadata is emitted to event/job logs, only stable `kind` + `reason` are canonical fields.
- `Cause` can be attached only to internal observability channel with redaction policy.
- Retry policy decision must use `Kind` class (retryable vs non-retryable), not string matching on raw cause.

### Invariants
- Idempotency/retry behavior cannot depend on unstable error text.

### Failure Semantics
- Mismatched retry classification due to text-based parsing is contract violation.

### Verification Evidence
- Worker/integration tests covering retry class decision for selected `Kind` values.

---

## 4) Error Contract (Core)

### Contract Item
- `APPERR-ERR-001` Shared envelope shape.

### Owner
- `pkg` shared layer.

### Rules
- Canonical envelope fields:
  - `Kind error` (domain class for HTTP/business mapping)
  - `Reason string` (stable reason code)
  - `Cause error` (primitive technical cause)
- `Unwrap()` MUST return `Kind` so `errors.Is` works.
- Envelope wrap helper MUST support `Wrap(kind, reason, cause)` style.

### Invariants
- `Kind` belongs to module-local `errorx`; shared package must not define domain sentinel of modules.
- `Reason` values are finite and documented per flow/module; no high-cardinality dynamic values.
- `Cause` may be nil; `Reason` must still be deterministic.

### Failure Semantics
- Missing kind: treat as internal contract failure and fallback to generic internal error.
- Empty/unstable reason where required: log as contract warning and map to module fallback reason.

### Verification Evidence
- Unit tests for envelope `errors.Is`, reason extraction, and nil-cause behavior.

### Contract Item
- `APPERR-ERR-002` Logging and redaction.

### Owner
- Handler/observability owners.

### Rules
- Structured logs should include: `error_kind`, `error_reason`, `error_cause` (internal only, sanitized if needed).
- Never log secrets/tokens/credentials/OTP/API key plaintext.
- Metrics labels use bounded `reason` sets only.

### Invariants
- Security-first: client-safe response, internal-rich diagnostics.

### Failure Semantics
- Logging raw secret or unbounded reason is sev-high observability/security defect.

### Verification Evidence
- Log snapshot tests/review + dashboard cardinality checks.

---

## 5) Permission Contract

### Contract Item
- `APPERR-PERM-001` Authorization of error detail visibility.

### Owner
- Module transport/ops policy owners.

### Rules
- End-user/client receives generic error according to endpoint policy.
- Internal operators (logs/monitoring access) can see `reason` and sanitized `cause` under existing RBAC for observability systems.
- No elevated permission path is granted by envelope itself.

### Invariants
- Error envelope does not change authz model; it only standardizes representation.

### Failure Semantics
- Unauthorized detail exposure is a policy breach.

### Verification Evidence
- RBAC review for log/monitoring access + API response contract tests.

---

## 6) Source-of-Truth Mapping

- Domain class source: `internal/<module>/errorx/*`.
- Shared envelope source: `pkg` shared package (planned path: `pkg/apperr`).
- HTTP mapping source: transport handlers.
- Observability labels source: stable `Reason` dictionary per module flow.

## 7) Adoption Scope (v1)

- Pilot module: IAM admin login/auth critical flows.
- Out-of-scope for v1:
  - full-system taxonomy rollout for all modules,
  - public API error schema expansion,
  - migration of all legacy error paths in one release.

## 8) Change Log Policy

- Any new `Reason` added for a public-critical flow MUST be documented as delta in related spec/plan.
- Breaking change examples requiring version bump note:
  - changing `Kind` meaning,
  - repurposing an existing `Reason` code,
  - exposing previously internal fields to client payload.
