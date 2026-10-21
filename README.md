# Virtual Bank Accounts

Manage "virtual bank accounts" for a family: managers deposit directly, users request deposits that a manager approves or rejects, and everyone can view balances and withdraw.

## Layout

| Folder | Contents |
|---|---|
| [spec/](spec) | OpenAPI specs: `auth.yaml` (login, logoff, password, list and revoke own sessions), `admin_family.yaml` (family admin: persons and accounts under `/family/admin`), `operations.yaml` (accounts, transactions, requests), `admin_operator.yaml` (platform operator: families, under `/admin`) |
| [db/](db) | SQLite migrations (`NNN_name.sql`) |
| [docs/](docs) | [Business rules](docs/business-rules.md), [operator tasks](docs/operator-tasks.md) and the schema diagram (`data-model.svg`) |
| [src/backend/](src/backend) | Go: `cmd/lambda` (Lambda wiring), `cmd/local` (local dev server), `api` (web routes), `jobs` (scheduled tasks), `data` (SQL queries, migrations, etc.) |
| [src/frontend/](src/frontend) | The web app (installable PWA): Vite, React, TypeScript, Mantine. See [Web app](#web-app) |
| [iac/](iac) | Terraform for AWS (`ca-central-1`) |

Planned: `src/android`.

## Architecture

SPA (S3) / mobile app → CloudFront → Lambda (Function URL) → SQLite file on EFS, with AWS Backup; EventBridge Scheduler triggers the daily funding. Estimated cost is about $0.01 a month at low traffic. See [aws-architecture.drawio](iac/aws-architecture.drawio).

## Roles

- **Operator** (`operator` table): platform super-admin. Create the first one with `aws lambda invoke --function-name vbank-api --cli-binary-format raw-in-base64-out --payload '{"task":"create-operator","name":"me","password":"12+ chars"}' out.json`; a forgotten password is reset the same way with `"task":"reset-operator-password"` (it also ends that operator's sessions). Creates families and their first admin via `admin_operator.yaml`, and resets a family admin's password (`PUT /admin/families/{familyId}/admins/{personId}/password`); sees no money data. All Lambda tasks (`create-operator`, `reset-operator-password`, `daily-funding`, `seed-poc`, `revoke-sessions`) are documented in [docs/operator-tasks.md](docs/operator-tasks.md).
- **Family admin** (`person.is_admin`): may call every route in `admin_family.yaml`, for their own family only.
- **Account manager** (`account_role.role = manager`): deposits and withdraws directly and approves or rejects requests for that account. An open account always keeps one open manager, and a closed account takes no deposits, withdrawals, requests or funding skips (enforced by triggers in `db/001_init.sql`).
- **Account user**: withdraws and requests deposits. Everyone logs in with family code, person id and a password (a short PIN is fine; minimum 6 characters, enforced by the API).
- Admin status gives no rights on an account; only an `account_role` row does.

Pending requests count toward the account total, and the posted balance and pending amount are both available separately. Login is rate limited (5 failures per person or 20 per IP in 15 minutes returns 429).

**Multitenancy:** one deployment, many families (`family` table). Login takes a family code and the token carries the family; no route accepts a family id. `account_role` has composite foreign keys on `(id, family_id)`, so cross-family membership is impossible, and the existing triggers extend that to requests, transactions and funding skips.

**Sessions:** log in with `remember: true` (a kid's own device) and the session never expires: it stays until revoked. Without it a session lasts 7 days (a shared computer), and operators always get 1 hour. A person lists their sessions with `GET /auth/sessions` (device name, last use) and ends one with `DELETE /auth/sessions/{id}`. A family admin does the same for any person in the family (`admin_family.yaml`, not implemented yet). Logoff, a password change or reset, and closing the person or family also end sessions, and the `revoke-sessions` task ([docs/operator-tasks.md](docs/operator-tasks.md)) cuts off one person or a whole family, for a lost device. Only a SHA-256 of each token is stored (`session.token_hash`), so a leaked database holds no usable tokens.

**Closing a family:** the operator soft-deletes it (`DELETE /admin/families/{familyId}` sets `family.closed_at`; `PATCH` with `closed: false` reopens). Data is kept and no zero balance is required. Members can't log in (`trg_closed_family_no_session`; the API also revokes their sessions), their accounts take no transactions, requests or skips, daily funding skips the family, and its code stays reserved.

## Daily auto-funding

An account with `daily_funding > 0` (cents, set by an admin) is deposited automatically every day at 23:59 in its family's `family.timezone` (IANA name, default `America/Toronto`, set by the operator). A manager can skip any day (`PUT /accounts/{id}/funding-skips/{date}`); with no skip, the account is funded. EventBridge Scheduler invokes the same Lambda hourly at :59 UTC with `{"task":"daily-funding","scheduledTime":...}`, and the job funds the families whose local time is then in the 23:00 hour; the run is idempotent (one funding row per account per day) and implemented by `data.FundDaily`. Funding deposits have `person_id` NULL and a `funding_date`. Weekends are funded and missed days are not backfilled.

## Configuration

The Lambda reads these environment variables, which `iac/main.tf` sets. Nothing else is read.

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `DB_PATH` | yes | none (the function fails at start without it) | SQLite file; `/mnt/db/bank.db` on the EFS mount |
| `ORIGIN_SECRET` | yes | none (every web request gets 403 without it) | Random value Terraform shares with CloudFront (`x-origin-secret` header), so the public Function URL only answers CloudFront |

Migrations are read from the `migrations` folder next to the binary (copy `db/*.sql`). The Lambda binary only runs on AWS; to run the API on your machine use the local server below.

### Run locally

`src/backend/cmd/local` serves the same routes as the Lambda (`auth.yaml` and `operations.yaml`) on a SQLite file, with no AWS. The seed data is described in [docs/operator-tasks.md](docs/operator-tasks.md#seed-poc). Run these from `src\backend`:

```powershell
# 1. Seed a family from a JSON file (the fields are described in docs/operator-tasks.md#seed-poc; *.local.json is git-ignored)
'{"task":"seed-poc","family":{"code":"POC","name":"POC Family"},"people":[{"name":"Parent 1","password":"parent1-password","isAdmin":true},{"name":"Child 1","password":"child1-password","isAdmin":false}]}' | Set-Content seed.local.json
go run ./cmd/local -task '@seed.local.json'     # prints the ids

# 2. Start the server (http://127.0.0.1:8080/api/v1)
go run ./cmd/local

# 3. In another terminal: list who can log in, then log in
curl.exe "http://127.0.0.1:8080/api/v1/auth/persons?familyCode=POC"
curl.exe -X POST http://127.0.0.1:8080/api/v1/auth/login -H "Content-Type: application/json" -d '{\"familyCode\":\"POC\",\"personId\":1,\"password\":\"parent1-password\"}'
curl.exe http://127.0.0.1:8080/api/v1/accounts -H "Authorization: Bearer <token from login>"
```

- Flags: `-addr` (default `127.0.0.1:8080`), `-db` (default `local.db`, created and migrated on first use), `-migrations` (default `../../db`), `-task` (run one job from [docs/operator-tasks.md](docs/operator-tasks.md) and exit, as JSON or `@file`).
- The seed is idempotent: edit the JSON and run it again to change passwords, roles or funding (details in [docs/operator-tasks.md](docs/operator-tasks.md#seed-poc)).
- Force the daily funding now (the scheduler only funds in the 23:00 hour of each family's timezone). Stop the server first if it is running, since both use `local.db`; `-task` takes JSON or `@file`, and PowerShell needs the inner quotes escaped:
  ```powershell
  go run ./cmd/local -task '{\"task\":\"daily-funding\",\"force\":true}'   # today's local date, prints {"funded":N}
  go run ./cmd/local -task '{\"task\":\"daily-funding\",\"force\":true,\"scheduledTime\":\"2026-10-19T14:00:00Z\"}'   # that moment's local date
  ```
  It only funds accounts with `daily_funding > 0` (set `family.dailyFunding` in the seed) that have no skip for that day, and a second run for the same day funds 0.
- Delete `local.db` to start over. `*.db` and `*.local.json` are git-ignored.
- Differences from AWS: there is no CloudFront or origin-secret check (so keep it on localhost), and login rate limiting uses your connection's address. To import the paths into Postman or Swagger UI, use the specs in `spec/` with that server URL.
- Another option is the [`go test`](src/backend/api/api_test.go) suite, which exercises every route against a temporary database.

## Web app

`src/frontend` is a single-page app that runs in a browser and installs to the home screen on Android and iOS (a PWA). It uses React, Mantine, TanStack Query and React Router, with English and French. It needs Node 20 or later. It talks to `/api/v1` on its own domain, so there is no CORS: Vite proxies `/api` to the local server in development, and CloudFront does the same in production.

Screens: log in (family code, then pick your name), accounts (always shown, even with one account), an account popup (balance, "will be funded today", deposit or request, withdraw, history), requests (managers approve or reject) and settings (language, password, devices, log off).

Run it locally (PowerShell, from `src\frontend\web`), with the backend from [Run locally](#run-locally) already started and seeded:

```powershell
npm install        # first time only
npm run dev        # http://localhost:5173
```

| Command | What it does |
|---|---|
| `npm run dev` | Dev server with hot reload; `/api` goes to `127.0.0.1:8080` |
| `npm test` | Unit and screen tests (Vitest and Testing Library) |
| `npm run gen` | Regenerates the API types in `src/gen/` from `spec/auth.yaml` and `spec/operations.yaml`; run it after a spec changes |

Notes:
- The site is a PWA, so use "Add to Home Screen" to install it.
- The session token is kept in `localStorage`. Log in with "Keep me logged in" on a child's device and the session lasts until it is revoked in Settings.
- Only the app shell is cached for offline use; balances and requests are always fetched live.
- Page refreshes on a path like `/requests` work because a CloudFront Function in `iac/main.tf` serves `index.html` for paths without a file extension (not for `/api/*`).
- If `npm test` times out starting workers, it is usually antivirus scanning the first run; run it again.

## Changing the database

Add `db/002_<name>.sql` (never edit a deployed file), run the tests, then `terraform apply`. The Lambda applies pending migrations on start, tracked in `PRAGMA user_version`, and copies the database to `bank.db.v<N>.bak` first.

## Status

The schema, API specs and infrastructure are done, and the daily funding and seed jobs are implemented and tested. `src/backend/api` serves the routes in `auth.yaml` and `operations.yaml`; a test fails if one of them is not routed. The web app covers the same routes. Nothing has been run on real AWS yet, and the web app has only been tested against mocked responses, not against the running server.

### API status (proof of concept)

The first release implements only the family member routes. Families, persons and accounts are created with the `seed-poc` task ([docs/operator-tasks.md](docs/operator-tasks.md)) instead of the admin APIs.

| Spec | Routes | Status |
|---|---|---|
| [auth.yaml](spec/auth.yaml) | person list, login, logoff, change password, list and revoke own sessions | Implemented (`src/backend/api/auth.go`) |
| [operations.yaml](spec/operations.yaml) | accounts, transactions, requests, funding skips | Implemented (`src/backend/api/ops.go`) |
| [admin_family.yaml](spec/admin_family.yaml) | family admin: persons and accounts | Not implemented, specified only; covered by `seed-poc` |
| [admin_operator.yaml](spec/admin_operator.yaml) | operator: families, family admin passwords | Not implemented, specified only; covered by `seed-poc` and the `create-operator` / `reset-operator-password` tasks |

Until the admin specs are built, the API-only rules that belong to their routes (a person can't close themselves, an account needs a manager on update, sessions revoked on person or family close) are not enforced beyond the DB triggers, and the specs and `docs/business-rules.md` describe the target design, not what runs.
