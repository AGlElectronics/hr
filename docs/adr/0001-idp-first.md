# ADR-0001: IdP is the source of truth for login

## Status

Accepted (scaffold phase).

## Context

HR is being built alongside, but independent of, the central portal / IAM
work ("Den" in the broader architecture). Both HR and future product modules
(Rally, Stash integration, etc.) need to agree on who owns login.

Two options were on the table:

1. **HR owns credentials.** HR issues usernames and passwords and other
   modules federate to HR.
2. **An external identity provider owns credentials.** HR provisions users
   into the IdP; applications authenticate against the IdP; HR has no login
   surface itself.

## Decision

**The identity provider is the source of truth for login.** HR's role is to
*author* human identities and *push* them (plus their desired groups) into
the IdP via a connector. HR never issues credentials, never serves a login
page for its own operators (deferred — see ADR-0003), and never sees
passwords or tokens.

Applications that need to authenticate a user (Stash, future Rally, future
operator UIs including HR's own) do so against the IdP directly. HR provides
the data the IdP needs to make that work; it does not sit in the auth path.

The planned IdP implementation is Keycloak, but the HR module only commits
to the *interface* defined in `internal/provisioners/idp.go`. Any OIDC-
capable provider that can accept the connector's calls satisfies the
contract.

## Consequences

**Positive**

- Applications work without HR being up: once a user is in the IdP, they can
  sign in to any dependent app whether or not the HR service is reachable.
- No credential-handling code paths inside HR; the module stays small and
  the security surface stays narrow.
- Swapping Keycloak for another OIDC provider is a connector replacement,
  not a product rewrite.

**Negative**

- HR becomes dependent on an external system being healthy to provision new
  people. We tolerate this because the IdP will be a hard dependency for
  every other module anyway.
- Approving a draft before the IdP has actually applied the change is an
  easy mistake. The scaffold mitigates this by only moving a draft into
  `pending_approval` after the provisioning job returns success.
