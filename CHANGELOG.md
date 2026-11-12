# Changelog

All notable changes to HR are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

Needs Den: 1.0.0 or later (Den MAJOR 1).

First release, `1.0.0`. Released versions are tagged `vMAJOR.MINOR.PATCH`;
the images `ghcr.io/aglelectronics/hr-backend`, `hr-frontend` and
`hr-migrate` are published as `MAJOR.MINOR.PATCH-BUILD`, where `BUILD`
counts rebuilds of the same version. Rebuilds get no entry here.

### Added

- `GET /api/portal/todos` for Den's portal. Den's backend calls it with
  HR's pairing credential and the person's issuer and subject. The list is
  empty until HR has personal to-dos. Not paired answers 404; a wrong
  credential answers 401.

- Register of volunteers and staff, with onboarding (details, department,
  which access they get), pausing, reactivation, banning and offboarding.
  HR creates or removes the sign-in accounts in Keycloak and triggers the
  set-password email.
- Three HR roles from IdP groups: admins (`HR_ADMIN_GROUPS`, default
  `hr-admin`) may do everything, viewers (`HR_VIEWER_GROUPS`, `hr-viewer`)
  only read, and department leads (`HR_LEAD_GROUPS`, `hr-lead`) read,
  onboard and change access only in the departments whose lead list includes
  them. Every API route checks its own permission; `GET /api/me` returns the
  caller's permissions. Nobody can change their own access, status or ban.
  The web app only shows what the signed-in user may use.
- Departments with default roles and access groups, a lead list (leads also
  get the `dept-<slug>-lead` group, which other modules may use), archive
  and restore. `DEPARTMENT_HOOKS` hear about create, update, archive and
  restore.
- Access groups: HR admins bundle module roles under one name ("Standard
  volunteer") on the new Access groups page. Onboarding assigns access
  groups; a person page shows which access group each role came from.
  Changing an access group updates everyone who has it in the background.
- Role catalogs are discovered from the modules paired with Den
  (`GET /api/v1/integrations/modules`). A module with an empty slug or no
  catalog URL is skipped. `ROLE_CATALOG_SOURCES` still replaces a URL and
  adds modules Den does not list; a standalone HR uses only that list.
  HR keeps the last catalog of each module for when it is offline, only
  assigns roles a catalog publishes, and marks roles a module stopped
  publishing as "retired" on the people holding them. Admin-type roles
  (`privileged` in the catalog, or named `admin` / `*-admin`) can only be
  assigned by HR admins.
- Completing an onboarding runs in the background: the page follows the
  job, failed attempts are retried, and onboardings stuck by a crash are
  recovered. Onboardings that are still drafts can be cancelled. A "Resend
  set-password email" action replaces the temporary password.
- `IDP_PROVISIONER_MODE`: `manage` (default) creates and deletes accounts,
  `link` only finds existing accounts (for a brokered or existing convention
  identity provider) and never creates, renames, disables or deletes them,
  `off` keeps records in HR only. `KEYCLOAK_GROUP_PARENT` and
  `KEYCLOAK_GROUP_PREFIX` place HR's groups by full path,
  `KEYCLOAK_USER_ATTRIBUTES` limits the attributes HR writes,
  `KEYCLOAK_SEND_PASSWORD_SETUP=false` skips the set-password email for
  people who sign in through the convention's SSO. Group members and users
  are read page by page.
- Sign-in settings (staff sign-in mode, SSO name and admin page, identity
  provider admin console address), taken from Den unless set in HR. In
  `brokered` and `existing` mode the offboarding form warns that the SSO
  account stays active.
- Review queue for accounts without an HR record: a scan of the identity
  provider (`REVIEW_SCAN_INTERVAL`, default 15 minutes, plus a "Check
  accounts now" button), self-registration by signed-in volunteers
  ("Waiting for a coordinator" at `/register`) and imported rows. HR admins,
  and leads for their department, accept, merge or reject each entry;
  rejecting disables the account. Only an email the identity provider
  verified counts as a match, and nothing is accepted automatically.
- Import volunteers from `.xlsx` or `.csv` into the review queue: column
  mapping with saved templates, a dry run with a downloadable error report,
  and undo of a batch while its rows are still open. HR admins only.
- Audit log page and `GET /api/audit` for HR admins, with field-level
  changes for person updates. Each entry records the actor's name, OIDC
  issuer and subject. Den pairing, sync and unpairing are HR-admin only and
  audited.
- Personal-data retention (defaults pending legal review): applicants are
  deleted after 6 months untouched, people paused for 24 months are
  offboarded, and the access history of offboarded people is kept for 12
  months. HR admins are emailed 14 days before (the addresses under Settings
  → Notifications, or everyone in the admin group); nothing is deleted when
  that email cannot be sent. Den can set the periods (Admin → Modules → HR).
- Google Workspace connector logic: an account per onboarded person,
  department groups that follow HR, suspension on offboarding, deletion a
  set number of days later with a reminder to HR admins first, and "Delete
  now". This version ships without the Google Workspace client, so the
  connector shows as unavailable; `GOOGLE_WORKSPACE_CREDENTIALS_FILE` is
  read and logged only. See
  [Google Workspace](https://aglelectronics.github.io/con-services/modules/hr/google-workspace/).
- `GET /api/internal/directory/groups` lists, for other modules (the wiki's
  share dialog), the identity provider groups HR manages: each department's
  group and lead group (`kind: department`) and the groups granted by access
  groups (`kind: access`). It needs the bearer token `HR_DIRECTORY_TOKEN`
  (at least 16 characters) and is off when that is not set.
- Offboarding hooks (`OFFBOARD_HOOKS`) receive `issuer`, `subject` and
  `external_id` (the identity provider's account id; also still sent as
  `idp_user_id`) and `username`.
- People, onboardings, the review queue and the audit log are paged and
  searchable; the people list returns only the fields it shows.
- Background timing: `JOB_INTERVAL` (10s) and `MAINTENANCE_INTERVAL` (1h).
- Settings for username and email templates, the Den connection, and
  training requirements for roles (qualifications from Learn).
- Calls to other modules: role and qualification catalogs
  (`ROLE_CATALOG_SOURCES`, `QUALIFICATION_CATALOG_SOURCES`), Learn holdings
  (`LEARN_HOLDINGS_URL`), and offboarding and department hooks
  (`OFFBOARD_HOOKS`, `DEPARTMENT_HOOKS`).
- `DEN_MODE=required` keeps the module locked until it is paired with Den:
  the API answers `503 den_pairing_required` (except health, brand, sign-in
  and the Den connection routes) and the public `GET /api/den/state`
  reports `{"mode","paired"}`. The default `standalone` runs without Den;
  any other value stops the backend at startup.
- The web app shows a "Connect this module to Den" page (admins) or
  "Waiting for Den" (everyone else) while the backend runs with
  `DEN_MODE=required` and is not paired yet.
- `TRUSTED_PROXY_CIDRS` (optional): comma-separated CIDRs of the proxies in
  front of the backend whose `X-Forwarded-For` entries are believed. The
  default, `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128,fc00::/7`,
  covers the Gateway, nginx and Caddy; change it only for a proxy outside
  the private ranges, or set `none` when no proxy is in front. See
  [Client IP addresses](https://aglelectronics.github.io/con-services/operations/production/#client-ip-addresses).
- The backend can serve HTTPS. Set `TLS_CERT_FILE` and `TLS_KEY_FILE` to a
  PEM certificate and private key; the backend re-reads both every minute,
  so a certificate renewed by cert-manager is used without a restart. With
  neither set it serves plain HTTP, which is what Docker Compose
  development uses. See
  [Internal TLS](https://aglelectronics.github.io/con-services/operations/internal-tls/).
- The web app reads its installation settings (sign-in mode, issuer, client
  id, scopes, API address) from `/config.json` when it starts, so the
  published `hr-frontend` image works for every installation. nginx serves
  the file from `/etc/conservices/config.json` with `Cache-Control: no-store`,
  so a change shows on the next page load. Without a mounted file the web app
  shows "Not configured yet"; a broken file gives a clear error page. See
  [Web app settings](https://aglelectronics.github.io/con-services/modules/hr/configuration/#web-app-settings-configjson).
- Kubernetes: Helm chart `deploy/charts/hr` `1.0.0` in `con-services`,
  released with the tag `hr/v1.0.0`. Internal TLS is on by default:
  cert-manager issues `hr-backend-tls` and `hr-frontend-tls`, the backend
  and the web app serve HTTPS inside the cluster (web app Service on port
  443, backend on 8080, both named `https`), and every in-cluster address
  uses `https://`. The cluster needs cert-manager, trust-manager and the
  platform's `conservices-internal-ca` issuer; without them set
  `internalTLS.enabled: false`. See
  [Set up HR](https://aglelectronics.github.io/con-services/modules/hr/setup/).
  The chart's `config:` sets `HR_ADMIN_GROUPS`, `HR_VIEWER_GROUPS`,
  `HR_LEAD_GROUPS` and `IDP_PROVISIONER_MODE`; role catalogs are discovered
  from Den, so `ROLE_CATALOG_SOURCES` is only an optional override. The
  backend may call every module namespace (`networkPolicy.moduleEgressNamespaces`)
  and admits the wiki's backend (`networkPolicy.allowFromNamespaces:
  [conservices-wiki]`) for the group directory. `existingSecret` may hold
  `HR_DIRECTORY_TOKEN`. `googleWorkspace.enabled` (off by default) mounts
  the service-account key from `googleWorkspace.existingSecret` and opens
  egress to Google's APIs (`networkPolicy.googleApis`). See
  [Google Workspace](https://aglelectronics.github.io/con-services/modules/hr/google-workspace/).
- Development: `infra/docker-compose.yml` mounts
  `infra/frontend-config/compose.json` (dev sign-in) as the web app's
  `/config.json`; set `FRONTEND_CONFIG` in `.env` to use another file.
  Building from source needs Go 1.27.3 or newer.
- CI on every pull request (tests, lint, Trivy scans) and signed images with
  an SBOM published to GHCR on version tags (linux/amd64; linux/arm64
  optional).

### Changed

- The Helm chart's image tags default to `1.0.0-1` (backend, frontend and migrate).
- Den's backend is admitted by `networkPolicy.den` (on by default) for the role catalog and portal to-dos. `allowFromNamespaces` no longer lists `conservices-den`.
- `DEN_ALLOWED_HOSTS` is in the configuration reference and `.env.example`. It limits pasted pairing URLs when `DEN_INTERNAL_URL` is empty.
- CI passes `den-major: "1"`. A release whose MAJOR is not 1 is refused once the shared `ci-v1` tag points at the workflow that has that input. Moving that tag is still a maintainer step.
- Standalone Compose uses PostgreSQL `postgres:16.15-alpine`.
- The Helm chart description says CI renders and lints the chart; the first production install is still pending.
- **BREAKING:** Sign-in defaults are now `AUTH_MODE=oidc` and
  `APP_ENV=production`. `AUTH_MODE=dev` is refused unless
  `APP_ENV=development` (Docker Compose and `.env.example` set
  `development`). The API accepts only access tokens whose audience is
  `OIDC_AUDIENCE` (default `hr-api`) and refuses ID tokens. The web app
  keeps tokens in memory and sends the access token; after a reload it
  signs in again silently. Set `OIDC_AUDIENCE=hr-api` and confirm the
  Keycloak audience mapper on `hr-spa` adds `hr-api` to the access token.
- **BREAKING:** HR groups are matched with `OIDC_GROUP_MATCH` (default
  `full`), `OIDC_GROUP_PREFIX` and only the claim `OIDC_GROUPS_CLAIM`
  (default `groups`). A grant stored as a full path no longer matches a
  token that carries only the last segment unless `OIDC_GROUP_MATCH=last`.
- **BREAKING:** Retention periods are stored and edited in days
  (`retention.applicant_days`, `retention.paused_days`,
  `retention.offboarded_audit_days`; defaults 180, 730 and 365). A
  migration converts existing `*_months` rows (days = months × 365 / 12,
  rounded). Den's `retention.inactive_users_days` overrides applicants and
  paused people, and `retention.offboarded_volunteers_days` overrides the
  access history, when the value is greater than 0.
- **BREAKING:** `GET /api/role-catalog` on other modules needs
  `HR_SERVICE_TOKEN` (at least 32 characters, the same value on HR and on
  Wiki, Finance, Rally, Tasks, Stash, Learn and Desk). HR sends it as a
  bearer token, honours `ETag` / `304`, and keeps `revision`, `privileged`,
  `dynamic` and `group_display`. HR publishes its own catalog on the same
  contract. Generate one with `openssl rand -hex 32` and put it in each
  module's secret.
- The Den card shows a revoked credential and **Pair again**. The stored
  link keeps `den_link.revoked_at` (migration, and `denlink`'s own upgrade).
- Den's SMTP module setting is used while HR is paired, when Den sets
  `smtp.host`. When Den also sets `password_set`, the password is fetched
  from Den for that send and is not stored in HR. `SMTP_PASSWORD` is used
  only when Den sets no mail server. `smtp.security` of `starttls`, `tls`
  or `none` is honoured.
- The Google Workspace connector uses the shared client when
  `GOOGLE_WORKSPACE_CREDENTIALS_FILE` is set: accounts for everyone,
  department groups, suspend on offboarding, and deletion after
  **Delete after** days with an email **Reminder** days before. Without
  the key file the connector stays unavailable.
- The web app's nginx applies the shared security headers only on the
  pages it serves. Origins come from `KEYCLOAK_ORIGIN`, `DEN_ORIGIN`,
  `S3_ORIGIN` and `EXTRA_CONNECT_SRC`. `EXTRA_FRAME_SRC` stays empty.
  `/silent-callback.html` is the one page the app may frame itself, so
  silent sign-in can finish; it sends `frame-ancestors 'self'` and
  `X-Frame-Options: SAMEORIGIN`. The Helm chart's nginx config includes
  the same location. Every other response stays unframeable.
  Address search is proxied through HR, so the browser's `connect-src
  'self'` covers Photon; the backend is allowed to call `photon.komoot.io`.

### Security

- Outside `APP_ENV=development` the backend refuses to start with a known
  development secret (for example `hr-provisioner-dev-secret` or
  `learn-service-dev-token`) in any secret setting or hook token, and refuses
  to fall back to the in-memory identity provider when Keycloak is not
  configured (set `IDP_PROVISIONER_MODE=off` to run without accounts).
- HR never stores or returns a temporary password; job results contain no
  secrets.
- The address lookup only reaches public addresses unless the host is listed
  in `OUTBOUND_ALLOWED_HOSTS`, and Settings shows which service receives what
  HR users type into address fields (by default the public Photon server run
  by komoot).
- Pausing or banning someone disables their account; reactivating or lifting
  the ban enables it again.
- The backend reads the client address from `X-Forwarded-For` only when
  the connection comes from a trusted proxy (`TRUSTED_PROXY_CIDRS`), and
  ignores `X-Real-IP` and `True-Client-IP`.
- On Kubernetes the database URL Den shows ends in
  `?sslmode=verify-full&sslrootcert=/etc/conservices-internal-ca/ca.crt`;
  the platform's PostgreSQL refuses connections without TLS.
- No image runs as root. The web app runs on
  `nginxinc/nginx-unprivileged:1.30.6-alpine` as UID 101 and listens on
  port 8080. All images are built with Go 1.27.3 and pass the CI image
  scan (Trivy, HIGH and CRITICAL issues that have a fix).
- A production build never falls back to dev sign-in: a `/config.json`
  without `authMode` means OIDC. `VITE_DEV_USER` only works on the dev
  server; production builds in dev sign-in mode always sign in as
  `dev-hr-admin`.
