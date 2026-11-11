# HR

> [!WARNING]
> ## **NOT READY FOR PRODUCTION**
>
> Con Services is under active development. Expect breaking changes, missing
> features and bugs. Do not use it for real events or real data yet.

HR is the people module of Con Services. It keeps the register of volunteers
and staff, onboards them into departments, and gives or takes away their
sign-in account and access in Keycloak. The full guide (setup, configuration,
API, admin guide, personal data) is on the documentation site:
[HR module](https://aglelectronics.github.io/con-services/modules/hr/). This
README is about getting the stack running locally.

- **Backend:** Go 1.27, chi, pgx with hand-written SQL.
- **Frontend:** React 19 + Rsbuild + TanStack Query + Tailwind.
- **Database:** PostgreSQL 16, migrated via Goose (`cmd/migrate` one-shot).
- **Object storage:** HR stores **no files** in this phase, so no S3 server is
  intentionally wired into the Compose stack. The `S3_*` env schema is
  still read by `config.Load` so the first file-upload feature can land
  without a config/code churn. If an S3 server (the suite uses SeaweedFS) gets added for local dev
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

## Sign-in and HR roles

Everyone signs in with OIDC (OpenID Connect) against the suite's Keycloak
realm (`AUTH_MODE=oidc`). What someone may do in HR depends on their groups:

| Group (default)   | HR role | What they can do |
| ----------------- | ------- | ---------------- |
| `hr-admin`        | admin   | Everything, including settings, access groups, imports, the audit log and assigning admin roles of other modules. |
| `hr-viewer`       | viewer  | Read people, departments and onboardings; change nothing. |
| `hr-lead`         | lead    | Read, onboard and change access only in the departments whose lead list includes them. |

Change the group names with `HR_ADMIN_GROUPS`, `HR_VIEWER_GROUPS` and
`HR_LEAD_GROUPS`. Nobody may change their own access, status or ban.
Signed-in people without an HR role see "No access to HR" and can send their
details to a coordinator (the review queue).

For local work without Keycloak, `AUTH_MODE=dev` takes the user from the
`X-Dev-User` header (default `dev-hr-admin`, an HR admin) and groups from
`X-Dev-Groups`. Dev sign-in is for development only. See
[Configuration](https://aglelectronics.github.io/con-services/modules/hr/configuration/)
for every setting; `.env.example` lists them with development values.

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
   never migrates the database itself; that is the `migrate` job's work.
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

## Backups

In the full Con Services stack, Den's operator backs up HR's database and
bucket every night along with everything else (see
[docs/operations.md](../docs/operations.md)).

**Run standalone, or on a database server Den's operator doesn't manage, HR
is not backed up by anything. That is the administrator's job:** schedule a
`pg_dump --format=custom` of the HR database and a copy of its bucket, encrypt
both, and keep them somewhere other than the server. HR holds personal data
(addresses, dates of birth, emergency contacts), so encrypt the backups and
limit who can read them.

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
│   │   ├── migrate/      # one-shot Goose runner
│   │   └── server/       # API; NEVER migrates
│   ├── internal/         # app, config, http (handlers), service (logic), provisioners, …
│   └── migrations/       # embedded via migrations/embed.go
├── docs/                 # ADRs + design docs
├── frontend/             # Rsbuild SPA
└── infra/                # Docker Compose stack + Dockerfiles + nginx
```
