# Operator tasks

Tasks are run by invoking the Lambda directly with a JSON payload that has a `task` key. They don't go through the web API, so they need AWS IAM permission to invoke the function (`lambda:InvokeFunction`). A web request can't trigger them: a Function URL event has no top-level `task`. Code: [`src/backend/jobs/jobs.go`](../src/backend/jobs/jobs.go).

Replace `vbank-api` below with your function name (Terraform names it `<name>-api`, default `vbank-api`).

| Task | Run by | Purpose |
|---|---|---|
| `create-operator` | You, by hand | Create a platform operator (the first one, or another) |
| `reset-operator-password` | You, by hand | Recover a forgotten operator password |
| `daily-funding` | EventBridge Scheduler, hourly | Fund accounts at the end of each family's day |
| `seed-poc` | You, by hand | Create or update the initial family, people, accounts and roles from a JSON payload (proof of concept, replaces the admin routes) |
| `revoke-sessions` | You, by hand | End one person's or a whole family's login sessions (lost device) |

An unknown `task` returns the error `unknown task "..."`.

## Invoking a task

```powershell
aws lambda invoke --function-name vbank-api --cli-binary-format raw-in-base64-out `
  --payload '{"task":"create-operator","name":"me","password":"at least 12 chars"}' out.json
Get-Content out.json
```

- On success `out.json` holds the result. On failure `aws` prints `FunctionError` and `out.json` holds `errorMessage`.
- The password is in the command line and in your shell history. Prefer a one-time password you then change, and delete `out.json`.
- Use `--region ca-central-1` if your CLI default is different.

## `create-operator`

Adds an operator who can log in at `POST /admin/login` ([admin_operator.yaml](../spec/admin_operator.yaml)) and create and manage families. Operators can't see accounts, balances or transactions.

| Field | Required | Rule |
|---|---|---|
| `name` | yes | Not empty, unique |
| `password` | yes | 12+ characters (bcrypt hash is stored) |

```json
{"task":"create-operator","name":"me","password":"at least 12 chars"}
```

Result: `{"operatorId": 1}`.

Errors: a missing name or a password shorter than 12 characters (`name required, password must be 12+ characters`), or a name that already exists (`UNIQUE constraint failed: operator.name`).

The operator logs in with a 1-hour token. The operator API limits the password to 72 characters at login, because bcrypt ignores anything longer, so don't create one longer than that.

## `reset-operator-password`

Recovery for a forgotten operator password; there is no web route for it. It sets a new password and deletes every session of that operator, so any open login ends.

| Field | Required | Rule |
|---|---|---|
| `name` | yes | An existing operator |
| `password` | yes | 12+ characters |

```json
{"task":"reset-operator-password","name":"me","password":"a new password 12+"}
```

Result: `{"operatorId": 1}`.

Errors: a password shorter than 12 characters, or an unknown name (`operator "x": sql: no rows in result set`). Nothing changes on an error.

Failed-login counters are not cleared. If the operator is rate limited (5 failures in 15 minutes, 429), they wait for the window to pass.

To reset a **family admin's** password, an operator uses the web route `PUT /admin/families/{familyId}/admins/{personId}/password` instead.

## `daily-funding`

Normally you don't run this. The EventBridge schedule `cron(59 * * * ? *)` (UTC, every hour at :59) invokes it with:

```json
{"task":"daily-funding","scheduledTime":"2026-10-19T03:59:00Z"}
```

| Field | Required | Rule |
|---|---|---|
| `scheduledTime` | no | RFC 3339, UTC; default now. The scheduler fills it in with `<aws.scheduler.scheduled-time>` |
| `force` | no | `true` funds the local day of `scheduledTime` even outside the 23:00 hour (manual runs and backfill) |

For each open family (closed families are skipped) whose local time (its `timezone`) at `scheduledTime` is in the 23:00 hour, every open account with `daily_funding > 0` and no `funding_skip` row for that local day gets one deposit. The result is `{"funded": N, "skipped": M}`: the accounts funded now and the accounts a manager skipped for that day. Accounts already funded earlier that day count in neither.

- **Safe to re-run.** At most one funding deposit per account per day (`ux_funding_once_per_day`), so a retry or a manual rerun funds nothing twice. The day comes from `scheduledTime`, not the clock, so a late retry still funds the right day.
- **Manual run or backfill for one day:** add `"force":true` (with a `scheduledTime` on that day for a backfill), or pass the `scheduledTime` that falls in the 23:00 hour of that family's local day. A missed day is not backfilled automatically. Weekends are funded.
- **A bad timezone** on one family is reported in the error (`family N: ...`), and the other families are still funded.
- Zones with a :30 or :45 offset are funded up to 45 minutes before their local midnight.
- If the call fails, the scheduler retries up to 3 times within an hour.

Example manual run:

```powershell
aws lambda invoke --function-name vbank-api --cli-binary-format raw-in-base64-out `
  --payload '{"task":"daily-funding","scheduledTime":"2026-10-20T03:59:00Z"}' out.json
```

That time is 23:59 on 2026-10-19 in America/Toronto (EDT), so it funds that day for Toronto families and does nothing for others.

## `seed-poc`

Creates or updates one family with its people, accounts and roles. The proof of concept implements only `auth.yaml` and `operations.yaml`, so this task stands in for the `admin_family.yaml` and `admin_operator.yaml` routes. Everything comes from the event.

### Example

A family with two parents and two children:

```json
{
  "task": "seed-poc",
  "family": {
    "code": "POC",
    "name": "POC Family",
    "timezone": "America/Toronto",
    "dailyFunding": 50
  },
  "people": [
    { "name": "Parent 1", "password": "parent1-password", "isAdmin": true },
    { "name": "Parent 2", "password": "parent2-password", "isAdmin": true },
    { "name": "Child 1", "password": "child1-password", "isAdmin": false },
    { "name": "Child 2", "password": "child2-password", "isAdmin": false }
  ]
}
```

This creates:

| Person | Account | Role on Child 1's account | Role on Child 2's account |
|---|---|---|---|
| Parent 1 | none | manager | manager |
| Parent 2 | none | manager | manager |
| Child 1 | `Child 1` | user | none |
| Child 2 | `Child 2` | none | user |

Both accounts get a daily funding of 50 cents.

Result: `{"familyId":1,"persons":{"Parent 1":1,"Parent 2":2,"Child 1":3,"Child 2":4},"accounts":{"Child 1":1,"Child 2":2}}`. Use the person ids at `POST /auth/login`.

### Fields

| Field | Required | Rule |
|---|---|---|
| `family.code` | yes | Typed at login |
| `family.name` | yes | |
| `family.timezone` | no | IANA name, default `America/Toronto`; unknown zones are refused |
| `family.dailyFunding` | no | Cents deposited daily into every account, default 0 (off), not negative |
| `people[].name` | yes | Unique within the payload |
| `people[].password` | yes | 6 to 72 characters (a bcrypt hash is stored) |
| `people[].isAdmin` | no | `true`: no account of their own, and manager of every account. `false` (default): gets one account named after them, on which they are the `user` |

At least one person must be an admin, or the accounts would have no manager. Every rule is checked before anything is written, and the changes run in one transaction: any error rolls everything back.

### Running it again

Re-running is safe and updates the data:

- The family is matched by `code`, people by name and accounts by owner name. Passwords, `isAdmin`, funding and roles are set to the payload's values, and nothing is duplicated. Use it to reset a forgotten password.
- It ends the sessions of the people in the payload.
- It never deletes: someone left out of the payload stays, and a demoted admin keeps the manager roles already granted. A new admin becomes manager of every account in the payload.
- It does not reopen a closed family, person or account.
- Changing `family.code` creates another family.

### Passwords

The passwords are in the payload, so they end up in your shell history or in the seed file. Keep the file out of git (`*.local.json` is ignored) and change the passwords after the first login.

## `revoke-sessions`

Ends login sessions at once. Family sessions can be created without an expiry (`remember: true`), so this is how you cut off a lost or stolen device when the person can't do it themselves with `DELETE /auth/sessions/{id}`. Until `admin_family.yaml` is implemented it is also the only manager-side revoke.

| Field | Rule |
|---|---|
| `personId` | Revoke every session of this person |
| `familyCode` | Revoke every session of everyone in that family |

Give exactly one of the two; both or neither is an error (`exactly one of personId or familyCode is required`).

```json
{"task":"revoke-sessions","personId":2}
```

Result: `{"revoked": N}`, the number of sessions deleted (0 if none or an unknown id or code). The revoked tokens stop working on their next request. It doesn't change passwords: if the device is stolen, also change the person's password (the login needs it again).

## Notes

- Tasks run on the same single database connection as the API (reserved concurrency 1), so a long task delays web requests slightly.
- Logs go to the CloudWatch log group `/aws/lambda/<name>-api`.
- To add a task, add a `case` in `jobs.Run` and a section here.
