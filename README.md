# HR

HR provisioning module for the Awoostria service stack. Owns the people
registry, employment drafts, approvals, and the outbound Keycloak / Google
Workspace provisioning jobs. See `docs/` for the full architecture; this
README is strictly about getting the stack running locally.

- **Backend:** Go 1.25, chi, pgx (hand-written queries for now — `sqlc.yaml`
  is wired for a later pass, see the note at the top of that file).
- **Frontend:** React 19 + Rsbuild + TanStack Query + Tailwind.
- **Database:** PostgreSQL 16, migrated via Goose (`cmd/migrate` one-shot).
- **Object storage:** HR stores **no files** in this phase, so MinIO is
  intentionally NOT wired into the Compose stack. The `S3_*` env schema is
  still read by `config.Load` so the first file-upload feature can land
  without a config/code churn. If MinIO ever gets added back for local dev
  it is **local-dev only** and must never ship to production (ADR-0004,
  ADR-0005).

## Reserved ports

| Service         | Host port | Container port |
| --------------- | --------- | -------------- |
| backend (API)   | **8110**  | 8080           |
| frontend (SPA)  | **3003**  | 80             |
| postgres        | **5436**  | 5432           |
| Adminer (local) | 8083      | 8080           |

Every module in this ecosystem binds `:8080` *inside* its container; the
host-side port is reserved per-module (HR = 8110) and the Compose file maps
`host:8110 -> container:8080`. The host ports here are chosen to avoid
colliding with `stash` (8080/3000/5432) and `rally` (8100/3002/5435) on the
same dev machine.

## Auth model (this phase)

HR has **no operator login**. The API is ungated locally; the only
middleware is `devuser`, which stamps `X-Dev-User` (defaults to
`dev-hr-admin`) on writes so audit rows carry an actor name. Real OIDC /
Keycloak-based auth is deferred — see ADR-0003.

## First-time boot

Requirements:

- Docker Desktop (or equivalent) with Compose v2.
- No local processes listening on the host ports above.

```sh
cd hr
docker compose -f infra/docker-compose.yml up --build
```

What happens:

1. `postgres` boots and reports healthy.
2. `migrate` (one-shot) applies all pending Goose migrations and exits 0.
3. `backend` starts on container `:8080`, published on host `:8110`. It
   does **not** auto-migrate (ADR-0003).
4. `frontend` builds the SPA, serves it from nginx on `:3003`, and proxies
   `/api/...` + `/livez` + `/readyz` to the backend over `backend:8080`.

Health probes:

```sh
curl -s http://localhost:8110/livez   # from host → backend direct
curl -s http://localhost:3003/livez   # from host → frontend → backend
curl -s http://localhost:3003/api/me  # dev identity echo
```

Admin UIs:

- Adminer DB browser (local overlay): `http://localhost:8083`
  (`System=PostgreSQL`, `Server=postgres`, `User=hr`, `Password=hr`,
  `Database=hr`).

To enable the local-only Adminer:

```sh
docker compose \
  -f infra/docker-compose.yml \
  -f infra/docker-compose.local.yml up --build
```

## Standalone image build

The Dockerfiles are context-agnostic — a plain

```sh
cd hr
docker build -f infra/Dockerfile.backend  -t hr-backend  .
docker build -f infra/Dockerfile.frontend -t hr-frontend .
docker build -f infra/Dockerfile.migrate  -t hr-migrate  .
```

works from the `hr/` directory. All three Dockerfiles expect the build
context to be `hr/` (not `hr/backend` or `hr/frontend`) so they can see
both the source tree and `infra/nginx.conf`.

## Working locally without Docker

```sh
# Backend
cd hr/backend
go mod tidy
DATABASE_URL='postgres://hr:hr@localhost:5436/hr?sslmode=disable' \
  go run ./cmd/migrate      # applies pending migrations
HTTP_ADDR=:8110 \
DATABASE_URL='postgres://hr:hr@localhost:5436/hr?sslmode=disable' \
  go run ./cmd/server       # pick ANY free host port; container default is :8080

# Frontend
cd hr/frontend
npm install
npm run dev                 # serves on :3003, proxies /api to :8110
```

## Repository layout

```
hr/
├── backend/
│   ├── cmd/
│   │   ├── migrate/      # one-shot Goose runner (ADR-0003)
│   │   └── server/       # API; NEVER migrates
│   ├── internal/         # app, config, http, service, repo, …
│   ├── migrations/       # embedded via migrations/embed.go
│   └── queries/          # sqlc queries (hand-rolled pgx today)
├── docs/                 # ADRs + design docs
├── frontend/             # Rsbuild SPA
└── infra/                # Docker Compose stack + Dockerfiles + nginx
```
