# ADR-0003: Operator login for the HR UI is deferred

## Status

Superseded (MVP suite).

## Context

The HR provisioning module was scaffolded ahead of the identity provider.
ADR-0003 chose to ship without operator login until Keycloak was ready.

## Decision (original)

Option 3: no login page; ungated API with `X-Dev-User` only.

## Supersession

The shared suite Keycloak (`conservices`) is now the IdP for HR as well:

- `AUTH_MODE=oidc` + SPA client `hr-spa` (same realm as Den / Stash / Events).
- Real user creation via Admin API client `hr-provisioner`.
- `AUTH_MODE=dev` + `X-Dev-User` remains for offline tests without Keycloak.

See root `docker-compose.yml` and `hr/.env.example`.
