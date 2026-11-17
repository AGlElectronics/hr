# ADR-0006: Stub Sign until an e-sign provider is connected

## Status

Accepted (toward HR 1.1). Provider choice is open; revisit after product
decision.

## Context

HR needs versioned onboarding forms (NDA, GDPR, and the rest), email links
to volunteers, and a gate so IdP accounts are created only after HR has
checked signatures. A qualified e-sign provider (DocuSeal, Documenso, a
QTSP, …) is not chosen yet. Shipping the workflow without a provider would
block onboarding UX; pretending the stub is legally binding would mislead
operators.

## Decision

1. Ship the **forms library**, per-volunteer attach, **Send for signing**,
   public **sign-hr** host, filled PDF preview, and **Approve after
   signatures** now.
2. The volunteer **Sign** button marks the session `signed` locally
   (workflow state only). It is **not** a qualified electronic signature
   and is not non-repudiation.
3. Document that clearly for operators
   ([Onboarding forms](https://aglelectronics.github.io/con-services/modules/hr/onboarding-forms/)).
4. When a provider is chosen, replace the stub behind the same session and
   document statuses; keep token hashing, cancel/expiry, and the approve
   gate.

## Consequences

**Positive**

- Operators can practise the full packet flow in development and early
  installs.
- Security hardening around links (hash at rest, TTL, rate limits, nginx
  allowlist) lands with the feature instead of after a provider.

**Negative / follow-ups**

- Production use for legally required signatures waits on a provider (or
  an explicit local policy that the stub is enough for that organisation).
- Staff must still check the filled PDF before Approve; the stub does not
  prove who clicked Sign.
