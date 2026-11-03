# ADR-0002: Connector boundary for external systems

## Status

Accepted (scaffold phase).

## Context

The HR module has to talk to at least two external systems — an identity
provider (Keycloak, planned) and a workspace / collaboration product
(Google Workspace, planned). Those targets may change (we may end up on a
different IdP; "workspace" could become multiple backends; we may add e.g.
badge printing). Letting their SDKs leak into service code would couple HR
to specific vendors forever.

## Decision

Every external system the HR module speaks to is accessed through an
**interface** declared in `internal/provisioners`. Service code only ever
holds that interface, never a concrete vendor SDK type. Each interface has
exactly two implementations in-tree:

1. A **real** implementation that talks to the vendor over the network. Not
   written yet; added when we wire the real IdP / workspace.
2. A **fake** implementation that runs in-process, is deterministic and
   logs every call. This is what the scaffold ships (`FakeIdP`,
   `FakeWorkspace`), and it is what tests use.

Rules the services must follow:

- Services depend on the interface, not on a struct type.
- Services never import a vendor SDK directly.
- Connector methods are idempotent on `HRPersonID` so retries are safe.
- Connectors never read HR tables or write to the HR database. Side effects
  of a provisioning run (job rows, state transitions) are persisted by the
  calling service, not by the connector.

Wiring happens exactly once, in `internal/app/app.go`, where `main` chooses
which implementation to pass into the HTTP layer.

## Consequences

**Positive**

- Swapping Keycloak for another IdP is a one-file change plus a config
  flip.
- Tests do not need a running Keycloak / Google Workspace; the fakes are
  fast and have no network.
- The HR service code reads the same regardless of whether the fake or the
  real connector is behind the interface.

**Negative**

- A thin indirection the first time a developer traces a call. Mitigated by
  keeping the connector package tiny.
- Fakes can drift from reality. We accept this risk for the scaffold and
  will add contract tests when the real connectors land.
