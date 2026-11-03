# HR domain model

> **Scope.** HR provisioning module only. See the sibling ADRs under
> [`adr/`](./adr) for architectural rationale (identity-first auth, connector
> boundary, deferred login, MinIO-is-dev-only, one bucket per module).
>
> **Status.** Scaffold. The scaffold includes everything described below but
> many surfaces are stubs — the fake IdP/workspace connectors run in-process,
> and the HTTP API is ungated (trivial `X-Dev-User` only).

## 1. Scope and non-goals

**In scope**

- Create people (stable internal identity).
- Author employment drafts and assign desired roles / groups.
- Trigger provisioning against the identity provider (IdP) and the user's
  productivity workspace.
- HR approves (or rejects) the resulting draft.
- Notify the newly-provisioned user that their account is live.
- Append-only audit of every state-changing write.

**Out of scope this phase**

- Operator login / OIDC for the HR UI — deferred (ADR-0003).
- Real Keycloak / Google Workspace connectors — fake implementations only.
- Deprovisioning / off-boarding flows.
- File uploads / document storage (object storage is wired for later; one
  bucket per module, `hr-data-dev`, dev-only MinIO).
- Multi-tenant / organisation support.

## 2. Entities

| Entity | Purpose | Notes |
| --- | --- | --- |
| **Person** | Stable internal identity for a human. | Not a login — the IdP owns credentials (ADR-0001). Survives role churn and departures. |
| **EmploymentDraft** | HR-authored proposed employment with lifecycle state. | Owns the state machine in §3. |
| **RolePlan** | Desired role / IdP group assignment for a draft. | Frozen once provisioning starts so the audit trail matches what was pushed. |
| **ProvisioningJob** | One attempt to push a draft to an external system (`idp` or `workspace`). | Append-only; retries create new rows. |
| **Approval** | HR's final decision on a draft (`approved`\|`rejected`). | One decision ends the draft lifecycle. |
| **AuditEvent** | Append-only record of every state-changing write. | Never stores secrets. |

Entity shapes are declared in Go at
[`backend/internal/domain/domain.go`](../backend/internal/domain/domain.go)
and in SQL at
[`backend/migrations/20261101000001_init.sql`](../backend/migrations/20261101000001_init.sql).

## 3. Draft state machine

```
    draft ──────► provisioning ──────► pending_approval ──┬─► active
                       │                                  │
                       │                                  └─► rejected
                       │
                       └─► rejected   (provisioning failed)
```

Rules:

- `draft`: HR is still editing. Roles are editable. No external system has
  been touched.
- `provisioning`: a provisioning run is in flight. Roles are frozen.
- `pending_approval`: provisioning succeeded. Waiting for HR to approve or
  reject.
- `active`: terminal success state. Notification is allowed from here.
- `rejected`: terminal failure state. A new draft is required to retry.

Transitions are enforced by `domain.Transition` in
[`backend/internal/domain/domain.go`](../backend/internal/domain/domain.go)
and used by every service that mutates draft state.

## 4. Interactions with external systems

The HR module speaks to the outside world through *connector interfaces* in
[`backend/internal/provisioners`](../backend/internal/provisioners). This
phase ships only the fake implementations:

| Connector | Interface | Real target (future) | Fake |
| --- | --- | --- | --- |
| Identity provider | `IdPProvisioner` | Keycloak | `FakeIdP` — in-memory, deterministic external IDs. |
| Workspace | `WorkspaceProvisioner` | Google Workspace | `FakeWorkspace` — in-memory, deterministic mailbox URL. |

See [ADR-0002](./adr/0002-connectors-boundary.md) for the boundary rules.

## 5. HTTP surface (stubs)

All endpoints live under `/api` and are **ungated** (trivial `X-Dev-User`
header stamps the actor for audit). See
[`backend/internal/http/router.go`](../backend/internal/http/router.go).

| Method | Path | Purpose |
| --- | --- | --- |
| GET  | `/livez` | Liveness probe. |
| GET  | `/readyz` | Readiness probe (DB ping). |
| GET  | `/api/me` | Echo the dev actor. |
| GET  | `/api/people` | List people. |
| POST | `/api/people` | Create person. |
| GET  | `/api/people/{id}` | Get person. |
| GET  | `/api/drafts` | List drafts (`?state=` filter). |
| POST | `/api/drafts` | Create draft. |
| GET  | `/api/drafts/{id}` | Get draft (includes role plans). |
| PUT  | `/api/drafts/{id}/roles` | Replace role plans (only in `draft`). |
| POST | `/api/drafts/{id}/provision` | Trigger provisioning. |
| GET  | `/api/drafts/{id}/provisioning-jobs` | List provisioning job attempts. |
| POST | `/api/drafts/{id}/approvals` | Record approval decision. |
| GET  | `/api/drafts/{id}/approvals` | List approval decisions. |
| POST | `/api/drafts/{id}/notify` | Notify user (log-only stub). |

## 6. Data model (quick reference)

Tables created in the initial migration:

- `people` — stable internal identity.
- `employment_drafts` — draft lifecycle + state column.
- `role_plans` — one row per desired role on a draft.
- `provisioning_jobs` — append-only job attempts per draft.
- `approvals` — approval decisions.
- `audit_events` — append-only audit trail.

The `goose_db_version` table is managed by Goose and lives in the same DB.
`cmd/migrate` is a one-shot container; the API server never auto-migrates.

## 7. Open questions / later phases

- Keycloak-backed `IdPProvisioner` and `WorkspaceProvisioner` for Google.
- Operator login (ADR-0003) — once the IdP is up HR UI picks up OIDC.
- Off-boarding / role revocation flow (new state machine).
- Pagination and search on `GET /api/people` and `GET /api/drafts`.
- Background job runner so provisioning is async rather than synchronous.
