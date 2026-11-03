# ADR-0004: MinIO is a local dev convenience, never production

## Status

Accepted (scaffold phase).

## Context

The HR Compose stack includes MinIO as an S3-compatible object store for
the module's single bucket (`hr-data-dev`, see ADR-0005). Developers can
run the whole stack with `docker compose up` without needing cloud
credentials or a shared bucket.

MinIO is attractive in local development. It is less attractive in
production: the operational burden (upgrades, backups, erasure coding,
multi-az placement) is non-trivial, and the convention application suite is
not a storage product. Running MinIO as "temporary" production storage has
a well-known trajectory of becoming permanent.

## Decision

**MinIO is for local development only.** HR must never ship with MinIO as
its production object store.

Production deployments must use a managed, provider-native bucket (AWS S3,
GCS via the S3 API, Backblaze B2, etc.). The code paths and configuration
schema do not change between dev and production — only the endpoint,
credentials and `S3_USE_PATH_STYLE` do — because the module only talks to
the common S3 API surface.

Specifically:

- The `minio` and `minio-init` services in `infra/docker-compose.yml` are
  labelled "LOCAL DEV ONLY" in a comment at the top of the file.
- The project README calls out that MinIO is dev-only.
- The config package treats S3 settings as optional and never implies that
  the local defaults are production-safe.

## Consequences

**Positive**

- Local dev stays "one command, zero cloud setup".
- No operational commitment to running MinIO at scale.
- Production object storage decisions stay with whoever owns the target
  cluster, not with HR module authors.

**Negative**

- Devs can accidentally reuse the MinIO credentials if they copy-paste
  `.env` into a production environment. We mitigate this by labelling and
  by using distinctly non-production bucket names (`hr-data-dev`).
