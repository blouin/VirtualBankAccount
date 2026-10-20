# Business rules

Where each rule is enforced: **DB** = constraint, index or trigger in [`db/001_init.sql`](../db/001_init.sql) (tested in `src/backend/data/migrate_test.go`); **API** = checked by the Lambda, see the specs in [`spec/`](../spec); **Job** = scheduled Lambda task. Schema: [data-model.svg](data-model.svg).

## Roles and tenancy

| Rule | Layer |
|---|---|
| Everything belongs to one family; a family is identified by its family code at login. | DB, API |
| The tenant comes from the session token. An id from another family returns 404. | API |
| `account_role` has composite FKs, so a person can only be a member of an account in their own family. | DB |
| Platform operators create families and the family admin, and use `admin_operator.yaml`. Operators are created by the `create-operator` task (password 12+ characters); a forgotten operator password is reset by the `reset-operator-password` task, which also ends their sessions. | API, Job |
| An operator can reset a family admin's password (`PUT /admin/families/{familyId}/admins/{personId}/password`, 8+ characters, the person must be an open admin of that family). Sessions are revoked. | API |
| Family member sessions last 7 days, fixed and not sliding, unless login sends `remember: true`: then `session.expires_at` is NULL and the session never expires until revoked. Operator sessions always last 1 hour. A session is valid when `expires_at IS NULL OR expires_at > now`. | API, DB (`session.expires_at` nullable) |
| Session tokens are 32 random bytes; only their SHA-256 (`session.token_hash`) is stored. | API, DB |
| A session ends by logoff, `DELETE /auth/sessions/{id}` (own sessions only, others 404), a password change or reset (the other sessions), the person or family being closed, or the `revoke-sessions` task (one person or a whole family). `GET /auth/sessions` lists the caller's live sessions with device and last use. A family admin lists and revokes any person's sessions in the family (`/family/admin/persons/{id}/sessions`). `last_used_at` is updated at most once an hour. | API, Job |
| A family admin (`person.is_admin`) manages persons and accounts via `admin_family.yaml`. This grants no rights on an account's money. | API |
| Account rights come only from `account_role.role`: `manager` (parent) or `user` (kid). | DB |
| Every person has a password (a short PIN is allowed, 6-character minimum). | DB (`NOT NULL`), API |

## Money

| Rule | Layer |
|---|---|
| Amounts are integer cents, greater than 0 (the API caps them at 100,000,000 = $1M). | DB, API |
| `balance` = posted transactions; `pending` = pending requests; `total` = balance + pending (what users see). All three are exposed. | DB view `account_balance` |
| A closed account stays in the view (with `closed_at`), so its history is still readable by its members. | DB view, API (`includeClosed`) |
| A withdrawal can only spend the posted balance, never pending requests (`insufficient funds`). | DB `trg_no_overdraft` |
| The ledger is append-only: transactions can't be updated or deleted, so a mistake is fixed by a new transaction. | DB `trg_transaction_immutable_upd`, `_del` |
| A deposit that cites a request must match that request: approved, same account, same amount. | DB `trg_transaction_matches_request` |

## Who can do what

| Action | Manager | User |
|---|---|---|
| View balance and transactions | yes | yes |
| Deposit directly | yes | no |
| Withdraw | yes | yes |
| Request a deposit | yes | yes |
| Approve or reject a request | yes | no |
| Skip or unskip a funding day | yes | no |

All of the above apply only to members of that account, and a closed person is refused everywhere.

| Rule | Layer |
|---|---|
| Direct deposit: manager only. Withdrawal: any member. | DB `trg_transaction_authorized` |
| Request: any member of the account. | DB `trg_request_by_member` |
| Decide a request: manager of that account only. | DB `trg_request_decided_by_manager` |
| Skip a funding day: manager only. | DB `trg_funding_skip_by_manager` |
| An account's money rules are unaffected by `is_admin`. | DB |

## Requests

| Rule | Layer |
|---|---|
| States: `pending`, `approved`, `rejected`. A pending request has no decider; a decided one has. | DB CHECK |
| A request can be decided only once (`request already decided`). | DB `trg_request_decided_once` |
| What the requester sent (account, amount, requester, time, comment, key) can't change, and a request is never deleted; only the decision columns change. | DB `trg_request_immutable_upd`, `_del` |
| `decided_at` is set exactly when the request is decided. | DB CHECK |
| Approval posts the deposit in the same statement as the status change. | DB `trg_request_approved` |
| Requester and decider comments are optional, at most 200 characters; a decider comment needs a decider. | DB CHECK |
| `Idempotency-Key` (1 to 64 characters) on transactions and requests: a replay returns the stored row with 200 instead of 201; the same key with a different account, type or amount is 409. | DB unique indexes, API |

## Persons

| Rule | Layer |
|---|---|
| Persons are never hard-deleted. Closing sets `closed_at`; history and memberships are kept. | DB, API |
| A closed person can't log in, has no session, and is not listed on the login page. Sessions are revoked on close or password reset. | DB `trg_closed_person_no_session`, API |
| A closed person can't be added to an account. | DB `trg_closed_person_no_role` |
| Closing is refused if the person is the only open manager of an open account. Having another open manager is fine. | DB `trg_person_close_not_only_manager` |
| A person can't close themselves. | API |
| A family always keeps one open admin: the last one can't be closed or demoted. | DB `trg_last_admin_kept` |
| Families are never hard-deleted. The operator closes one (`DELETE /admin/families/{id}`, sets `family.closed_at`) or reopens it (`PATCH closed:false`). No zero balance is required; data stays intact. | DB, API |
| Members of a closed family can't log in; their sessions are revoked on close; `/auth/persons` is empty for it. | DB `trg_closed_family_no_session`, API |
| Daily funding skips closed families. The family code stays reserved. | Job, DB (`UNIQUE`) |
| A family admin can reset any person's password, so can act as that person. This is intended. | API |
| Password minimum is 6 characters and maximum 72 (bcrypt ignores the rest); an admin password reset can't be null. | API |

## Accounts

| Rule | Layer |
|---|---|
| Accounts are never hard-deleted. Closing sets `closed_at`. | DB, API |
| Closing is rejected while the balance is not 0 or requests are pending. | API |
| Closed accounts are listed only with `includeClosed`, but their detail and history stay readable by members. | API |
| Approving on a closed account is refused (409); a request can't be approved on an account that was closed meanwhile. | DB `trg_account_open_for_transaction` |
| A closed account, or an account of a closed family, takes no transactions (including approvals and funding), requests or funding skips (`account or family is closed`). | DB `trg_account_open_for_transaction`, `_request`, `_skip` |
| An open account always keeps one open manager: the last manager can't be removed or demoted. Closed accounts are exempt. | DB `trg_last_manager_not_removed`, `trg_last_manager_not_demoted` |
| Creating an account needs at least one manager (a trigger can't see members still to come). | API |
| Updating members must keep a manager, and must be a diff: insert or change first, delete last, because triggers fire row by row. | API |

## Daily funding

| Rule | Layer |
|---|---|
| `account.daily_funding` (cents, 0 = off) is set per account by the family admin. There is no global value. | DB, API |
| Every day at 23:59 in the family's time zone (`family.timezone`, IANA, default `America/Toronto`, set by the operator) each open account with `daily_funding > 0` is funded. Weekends are funded; missed days are not backfilled. | Job |
| The scheduler runs hourly at :59 UTC and funds the families whose local hour is then 23. | Job |
| At most one funding deposit per account per day. A rerun does nothing. | DB `ux_funding_once_per_day` |
| Funding deposits have no person and have a `funding_date`; a direct deposit has a person and no `funding_date`. | DB CHECK |
| A manager can skip one day for one account (a `funding_skip` row); other accounts in the family are still funded. The skip can be removed. | DB, API |
| The skip date must be today or later in the family's time zone. Today can be skipped until the 23:59 run. | API |

## Login and security

| Rule | Layer |
|---|---|
| Login takes family code, person and password. | API |
| Rate limit: 5 failures for one person or operator, or 20 for one IP, in 15 minutes gives 429. | DB view `login_failures_15m`, API |
| Requests without the CloudFront `x-origin-secret` header get 403. | API |
