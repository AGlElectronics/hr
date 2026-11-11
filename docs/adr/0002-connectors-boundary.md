# ADR-0002: Connector boundary for external systems

## Status

Accepted. Updated for 1.0.0: the Keycloak connector is real, the fake
workspace job is gone, and the Google Workspace connector logic is in HR
against an interface whose real client comes from the shared Go package.

## Context

The HR module talks to an identity provider (Keycloak) and, optionally, to
Google Workspace. Those targets may change (another IdP, an IdP HR does not
own, more workspace products). Letting their SDKs leak into service code
would couple HR to specific vendors.

## Decision

Every external system HR speaks to is accessed through an **interface** in
`internal/provisioners`. Service code holds the interface, never a vendor
type.

- **Identity provider.** `IdPProvisioner` creates or links accounts and sets
  groups; smaller interfaces (`AccountManager`, `Directory`, `UserScanner`,
  `GroupEnsurer`, `PasswordSetupSender`, `UsernameDirectory`) add what only
  some implementations offer, and services check for them with a type
  assertion. The real implementation is `KeycloakIdP` (Admin API through a
  service-account client). `IDP_PROVISIONER_MODE` chooses `manage` (HR owns
  accounts), `link` (HR finds existing accounts and never creates or deletes
  them) or `off` (no connector; HR keeps records only).
- **Google Workspace.** `WorkspaceDirectory` (users create/suspend/delete/get,
  groups and members). HR's logic (account per person, department groups,
  suspend on offboarding, delayed delete with reminders) is written and
  tested against `FakeWorkspace`. The real HTTP client is not part of HR; it
  is plugged in from the shared Go package (`shared/go/gworkspace`) through a
  `WorkspaceFactory`. Until then the connector reports itself unavailable.
- **Fakes are for tests only.** `FakeIdP` and `FakeWorkspace` run in-process
  and are deterministic. Outside `APP_ENV=development` HR refuses to start
  rather than fall back to the fake identity provider, and it never records a
  fake job as succeeded.

Rules the services follow:

- Services depend on interfaces, never on a struct type or vendor SDK.
- Connector methods are idempotent (keyed on the HR person id or the email),
  so background job retries are safe.
- Connectors never read or write HR tables. Job rows, state transitions and
  audit events are written by the calling service.

Wiring happens once, in `internal/app/app.go`.

## Consequences

**Positive**

- Supporting another IdP or an existing convention IdP is a new
  implementation plus configuration, not a change to the services.
- Tests need no running Keycloak or Google Workspace; the Keycloak connector
  itself is tested against an `httptest` fake of the Admin API.

**Negative**

- A thin indirection when tracing a call.
- Fakes can drift from reality; the Keycloak connector's tests and the smoke
  script against a real Keycloak (`scripts/smoke-hr-keycloak.ps1` in the root
  repository) cover that for the IdP. The Workspace client gets its contract
  tests in the shared package.
