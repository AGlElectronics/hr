# HR domain model

> **Scope.** The HR module only. The user and operator documentation is on the
> documentation site ([HR module](https://aglelectronics.github.io/con-services/modules/hr/));
> this page explains the model for developers. Architectural decisions are in
> [`adr/`](./adr).

## 1. What HR does

- Keeps the register of volunteers and staff (**people**) with their contact
  details, emergency contact and status.
- Onboards people into **departments**: an onboarding (an *employment draft*)
  says which department and which access someone gets.
- Gives and takes away sign-in accounts and access in the identity provider
  (Keycloak) by putting people into groups. Which groups exist comes from the
  **role catalogs** other modules publish.
- Pauses, bans and offboards people; offboarding deletes their account and
  their HR record and keeps only the access history for a limited time.
- Reviews accounts that exist without an HR record (the **review queue**),
  fed by a periodic scan, self-registration and spreadsheet imports.
- Records every change in the **audit log**.

Out of scope: storing files, payroll, scheduling (Rally), and the Google
Workspace HTTP client (HR has the connector logic; the client is plugged in
later, see [ADR-0002](./adr/0002-connectors-boundary.md)).

## 2. Entities

| Entity | Table | Purpose |
| --- | --- | --- |
| **Person** | `people` | A human HR knows. Not a login: the identity provider owns credentials ([ADR-0001](./adr/0001-idp-first.md)). Stores the account id (`idp_user_id`) and, separately, the OIDC subject (`idp_subject`), since they differ with brokered or existing identity providers. |
| **Department** | `departments`, `department_leads` | A team with its own IdP group (`dept-<slug>`), a lead group (`dept-<slug>-lead`), default roles and access groups, and a lead list. Can be archived. |
| **EmploymentDraft** (onboarding / membership) | `employment_drafts` | One person in one department. While `draft` it is an onboarding; once `active` it is a membership. |
| **RolePlan** | `role_plans` | One module role on a draft (`<module>:<role key>` and its group), picked individually or expanded from an access group. |
| **Access group** (bundle) | `access_bundles`, `draft_bundles` | A named set of module roles HR admins define, e.g. "Standard volunteer". |
| **Catalog cache** | `catalog_cache` | The last role catalog each module published, used while the module is offline. |
| **Background job** | `background_jobs` | Provisioning, access-group updates and other work that runs outside the request, with attempts and a result. |
| **Review item** | `review_items` | An account, self-registration or imported row without an HR record, waiting for a human decision. |
| **Import batch / template** | `import_batches`, `import_templates` | One spreadsheet import (undoable while its rows are open) and saved column mappings. |
| **Role requirement / pending grant** | `role_requirements`, `pending_role_grants` | Training a role needs (from Learn) and roles held back until the training is done. |
| **Workspace account** | `workspace_accounts` | A person's Google Workspace account and when it is deleted after offboarding. |
| **Settings** | `settings` | Key/value settings (templates, identity, retention, Workspace, notifications). |
| **AuditEvent** | `audit_events` | Append-only record of each change: actor name, issuer and subject, action, field-level diffs. Access history of offboarded people gets a `purge_after` date. |

Entity shapes are in [`backend/internal/domain/domain.go`](../backend/internal/domain/domain.go)
and the migrations under [`backend/migrations`](../backend/migrations).

## 3. Person status

| Status | Meaning |
| --- | --- |
| `applicant` | Known to HR, not onboarded anywhere yet. |
| `onboarding` | Has an onboarding in progress. |
| `active` | Has at least one active membership; can sign in. |
| `paused` | Temporarily inactive: the account is disabled, access is kept. |

A ban (`is_banned`) is separate from the status: it disables the account and
blocks new onboardings until lifted. Offboarding deletes the person.

## 4. Onboarding (draft) state machine

```
    draft ─────► provisioning ─────► active
      │              │  ▲
      │              │  └── (job failed: back to draft, editable again)
      ▼              ▼
   rejected       rejected
```

- `draft`: HR is editing. Nothing in Keycloak was touched. Cancelling moves it
  to `rejected`.
- `provisioning`: a background job creates or links the account, sets groups
  and attributes and sends the set-password email. Attempts are counted;
  onboardings stuck here are recovered by the maintenance task.
- `active`: done; the draft is now a membership whose access can be changed.
- `rejected`: terminal; start a new onboarding to try again.

Transitions are enforced by `domain.Transition`.

## 5. External systems

Every external system sits behind an interface in
[`backend/internal/provisioners`](../backend/internal/provisioners)
([ADR-0002](./adr/0002-connectors-boundary.md)):

| Connector | Interface | Real implementation | Test fake |
| --- | --- | --- | --- |
| Identity provider | `IdPProvisioner` (+ `AccountManager`, `Directory`, `UserScanner`, …) | `KeycloakIdP` (Admin API, modes `manage` and `link`) | `FakeIdP` |
| Google Workspace | `WorkspaceDirectory` | not in HR yet (comes from the shared Go package) | `FakeWorkspace` |

Other modules are reached over HTTP: role catalogs (`/api/role-catalog`,
discovered through Den), Learn's qualification holdings, and the
`OFFBOARD_HOOKS` / `DEPARTMENT_HOOKS` webhooks. Other modules call HR's
`GET /api/internal/directory/groups`.

## 6. Authorization

HR roles come from IdP groups: admin (`hr-admin`), viewer (`hr-viewer`) and
lead (`hr-lead`). Each route checks a permission
([`backend/internal/authz`](../backend/internal/authz)); leads hold their
permissions only for departments whose lead list includes them. Nobody may
change their own access, status or ban.

## 7. Migrations

The `goose_db_version` table is managed by Goose. `cmd/migrate` is a one-shot
container; the API server never migrates.
