# ADR-0005: One bucket per module

## Status

Accepted (scaffold phase).

## Context

Each module in the convention application suite (Stash, HR, future Rally,
etc.) may need object storage. Two patterns are common:

1. **One shared bucket** with per-module key prefixes.
2. **One bucket per module.**

Shared buckets are convenient to provision once but entangle IAM, lifecycle
policies, versioning, and quota — a mis-scoped policy on a shared bucket is
a cross-module incident.

## Decision

Each module gets **its own bucket**. HR's bucket is `hr-data-dev` in local
development; the production equivalent will carry the module name in the
same way the module names its database.

Rules:

- Exactly one bucket per module. Multiple buckets per module (e.g.
  "photos", "exports", "backups") require a new ADR or a product-level
  decision.
- Module-scoped keys inside the bucket may still use prefixes
  (`drafts/<id>/…`, `people/<id>/…`) to keep storage organised.
- The bucket name is set via `S3_BUCKET`; the module logs it on boot so a
  mis-pointed deployment is loud.

HR currently has little to put in `hr-data-dev` beyond future document
uploads, but the bucket is created by `minio-init` on first `up` so the
plumbing exists and later file-upload work doesn't need to touch Compose.

## Consequences

**Positive**

- IAM scopes stay per-module; a mistake on one module cannot leak another.
- Lifecycle, versioning and encryption are per-module decisions.
- Easier to delete or archive a module cleanly — delete the bucket.

**Negative**

- Slightly more bucket management overhead when a new module appears.
  We accept this as a small, one-time cost.
