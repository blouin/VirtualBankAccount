-- Virtual bank accounts — SQLite schema
-- Money is stored as INTEGER cents (no float rounding).
-- Applied by src/backend/data/migrate.go, which turns foreign_keys back ON afterwards.

-- Tenant. Everything below belongs to exactly one family; the API takes the family from the session
-- token, never from request parameters. code = what a person types at login to pick the family.
-- closed_at = soft delete by the operator: data stays readable to the operator and can be reopened, but nobody in
-- the family can log in and no money moves (triggers below, and the funding job skips it). The code stays reserved.
CREATE TABLE family (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    code       TEXT    NOT NULL UNIQUE,
    timezone   TEXT    NOT NULL DEFAULT 'America/Toronto', -- IANA name; the family's day for daily funding and funding skips
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    closed_at  TEXT
);

-- Platform super-admin ("admin of admins"): only creates and lists families and their first admin
-- (admin_operator.yaml). Sees no accounts, balances or transactions.
CREATE TABLE operator (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE operator_session (
    token       TEXT    PRIMARY KEY,
    operator_id INTEGER NOT NULL REFERENCES operator(id) ON DELETE CASCADE,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    expires_at  TEXT    NOT NULL
);

-- Everyone logs in with family code + personId + password (a short PIN is fine; the API enforces a minimum
-- length of 6, SQL only sees the hash).
-- is_admin = admin of THIS family only: may call every admin_family.yaml route.
-- closed_at = soft delete (like account): history and memberships are kept. Triggers refuse a new session
-- for a closed person and every account action by them; the API revokes sessions and hides them from lists.
CREATE TABLE person (
    id            INTEGER PRIMARY KEY,
    family_id     INTEGER NOT NULL REFERENCES family(id),
    name          TEXT    NOT NULL,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
    closed_at     TEXT,
    UNIQUE (family_id, name),
    UNIQUE (id, family_id)
);

-- closed_at = soft delete, so transaction history is never lost.
-- daily_funding = cents deposited automatically at the end of each day (0 = off), unless a manager
-- added a funding_skip row for that day.
CREATE TABLE account (
    id            INTEGER PRIMARY KEY,
    family_id     INTEGER NOT NULL REFERENCES family(id),
    name          TEXT    NOT NULL,
    daily_funding INTEGER NOT NULL DEFAULT 0 CHECK (daily_funding >= 0),
    created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
    closed_at     TEXT,
    UNIQUE (id, family_id)
);

-- manager (parent) deposits directly and approves/rejects; user (child) must request.
-- The composite FKs make cross-family membership impossible. Requests, transactions and skips need no
-- family_id of their own: their triggers require the person to be a member/manager of the account.
CREATE TABLE account_role (
    account_id INTEGER NOT NULL,
    person_id  INTEGER NOT NULL,
    family_id  INTEGER NOT NULL,
    role       TEXT    NOT NULL CHECK (role IN ('manager', 'user')),
    PRIMARY KEY (account_id, person_id),
    FOREIGN KEY (account_id, family_id) REFERENCES account(id, family_id) ON DELETE CASCADE,
    FOREIGN KEY (person_id, family_id)  REFERENCES person(id, family_id)  ON DELETE CASCADE
);
CREATE INDEX ix_account_role_person ON account_role(person_id);

CREATE TABLE transaction_request (
    id           INTEGER PRIMARY KEY,
    account_id   INTEGER NOT NULL REFERENCES account(id),
    amount       INTEGER NOT NULL CHECK (amount > 0),
    status       TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    requested_by INTEGER NOT NULL REFERENCES person(id),
    decided_by   INTEGER REFERENCES person(id),
    requested_at TEXT    NOT NULL DEFAULT (datetime('now')),
    decided_at   TEXT,
    requester_comment TEXT CHECK (length(requester_comment) <= 200), -- optional note from the requester
    decider_comment   TEXT CHECK (length(decider_comment) <= 200),   -- optional note from the approver/rejecter
    idempotency_key   TEXT CHECK (length(idempotency_key) BETWEEN 1 AND 64), -- client's Idempotency-Key header
    CHECK (decider_comment IS NULL OR decided_by IS NOT NULL),
    CHECK ((status = 'pending') = (decided_by IS NULL)),
    CHECK ((status = 'pending') = (decided_at IS NULL))
);
-- A replayed POST /requests with the same key by the same person hits this index; the API returns the stored row.
CREATE UNIQUE INDEX ux_request_idempotency ON transaction_request(requested_by, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX ix_transaction_request_account_status ON transaction_request(account_id, status);

-- Ledger. amount is always positive; type gives the direction.
-- request_id is set when the deposit came from an approved request.
-- funding_date (YYYY-MM-DD) marks the automatic daily deposit for that day; those rows have no person.
CREATE TABLE account_transaction (
    id           INTEGER PRIMARY KEY,
    account_id   INTEGER NOT NULL REFERENCES account(id),
    person_id    INTEGER REFERENCES person(id),
    type         TEXT    NOT NULL CHECK (type IN ('deposit', 'withdrawal')),
    amount       INTEGER NOT NULL CHECK (amount > 0),
    request_id   INTEGER UNIQUE REFERENCES transaction_request(id),
    funding_date TEXT    CHECK (date(funding_date) IS funding_date),
    idempotency_key TEXT CHECK (length(idempotency_key) BETWEEN 1 AND 64), -- client's Idempotency-Key header
    comment      TEXT    CHECK (length(comment) <= 200), -- optional note; an approved deposit copies the requester's comment
    created_at   TEXT    NOT NULL DEFAULT (datetime('now')),
    CHECK (request_id IS NULL OR type = 'deposit'),
    CHECK (funding_date IS NULL OR (type = 'deposit' AND request_id IS NULL)),
    CHECK ((funding_date IS NULL) = (person_id IS NOT NULL))
);
-- Automatic funding runs at most once per account per day, so re-running the job is harmless.
CREATE UNIQUE INDEX ux_funding_once_per_day ON account_transaction(account_id, funding_date) WHERE funding_date IS NOT NULL;
-- A replayed POST /accounts/{id}/transactions with the same key by the same person hits this index.
CREATE UNIQUE INDEX ux_transaction_idempotency ON account_transaction(person_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
-- Serves GET /accounts/{id}/transactions (newest first, paged) and the latest-N list on GET /accounts/{id}.
CREATE INDEX ix_account_transaction_account ON account_transaction(account_id, created_at DESC, id DESC);

-- A row = "do NOT fund this account on this day"; no row = fund. Local date, YYYY-MM-DD.
-- Only a manager of the account can add one (trigger). The API enforces "today or later" and who may
-- delete, because SQLite knows neither the family's time zone nor the caller on DELETE.
CREATE TABLE funding_skip (
    account_id INTEGER NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    skip_date  TEXT    NOT NULL CHECK (date(skip_date) IS skip_date),
    created_by INTEGER NOT NULL REFERENCES person(id),
    comment    TEXT    CHECK (length(comment) <= 200), -- optional reason
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (account_id, skip_date)
);

-- Login sessions; DELETE row = logoff or revoke. expires_at NULL = never expires (login with remember = true):
-- the session lasts until someone revokes it. A session is valid when expires_at IS NULL OR expires_at > datetime('now').
-- token_hash is the SHA-256 (hex) of the 32 random bytes handed to the client; the raw token is never stored.
-- id is what GET /auth/sessions shows and DELETE /auth/sessions/{id} takes.
CREATE TABLE session (
    id           INTEGER PRIMARY KEY,
    token_hash   TEXT    NOT NULL UNIQUE,
    person_id    INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
    device       TEXT    CHECK (length(device) <= 100), -- optional label from the client, e.g. "Anna's iPad"
    created_at   TEXT    NOT NULL DEFAULT (datetime('now')),
    last_used_at TEXT,                                  -- the API updates it at most once an hour
    expires_at   TEXT                                   -- NULL = no expiry
); -- family = person.family_id (join)
CREATE INDEX ix_session_person ON session(person_id);

-- Login rate limiting: the API counts failed rows in a sliding window before checking credentials
-- (5 failures per person_id / operator_id or 20 per ip in 15 min -> 429). Both NULL = unknown family/person.
-- Used by POST /auth/login, PUT /auth/changePassword and POST /admin/login.
-- ponytail: the per-IP limit on GET /auth/persons is a plain request counter kept in API memory, not here;
-- move it to a table if the API runs on more than one process.
-- ponytail: rows grow forever; purge with DELETE ... WHERE attempted_at < datetime('now','-1 day') if it matters.
CREATE TABLE login_attempt (
    id           INTEGER PRIMARY KEY,
    person_id    INTEGER REFERENCES person(id) ON DELETE CASCADE,
    operator_id  INTEGER REFERENCES operator(id) ON DELETE CASCADE,
    ip           TEXT    NOT NULL,
    succeeded    INTEGER NOT NULL CHECK (succeeded IN (0, 1)),
    attempted_at TEXT    NOT NULL DEFAULT (datetime('now')),
    CHECK (person_id IS NULL OR operator_id IS NULL)
);
CREATE INDEX ix_login_attempt_person ON login_attempt(person_id, attempted_at);
CREATE INDEX ix_login_attempt_ip     ON login_attempt(ip, attempted_at);

-- Failed logins in the last 15 minutes; the API rejects with 429 when a limit is hit.
CREATE VIEW login_failures_15m AS
SELECT 'person' AS kind, CAST(person_id AS TEXT) AS key, COUNT(*) AS failures, MIN(attempted_at) AS oldest
FROM login_attempt
WHERE succeeded = 0 AND person_id IS NOT NULL AND attempted_at > datetime('now', '-15 minutes')
GROUP BY person_id
UNION ALL
SELECT 'operator', CAST(operator_id AS TEXT), COUNT(*), MIN(attempted_at)
FROM login_attempt
WHERE succeeded = 0 AND operator_id IS NOT NULL AND attempted_at > datetime('now', '-15 minutes')
GROUP BY operator_id
UNION ALL
SELECT 'ip', ip, COUNT(*), MIN(attempted_at)
FROM login_attempt
WHERE succeeded = 0 AND attempted_at > datetime('now', '-15 minutes')
GROUP BY ip;

-- balance = posted money, pending = awaiting approval, total = balance + pending (what users see).
-- Closed accounts are included (closed_at set) so their history stays readable; they can't take new money.
CREATE VIEW account_balance AS
SELECT account_id, name, closed_at, balance, pending, balance + pending AS total
FROM (
    SELECT a.id AS account_id,
           a.name,
           a.closed_at,
           COALESCE((SELECT SUM(CASE t.type WHEN 'deposit' THEN t.amount ELSE -t.amount END)
                     FROM account_transaction t WHERE t.account_id = a.id), 0) AS balance,
           COALESCE((SELECT SUM(amount) FROM transaction_request r WHERE r.account_id = a.id AND r.status = 'pending'), 0) AS pending
    FROM account a
);

-- Account-level authorization comes from account_role, not person.is_admin.

-- Closing a person (person.closed_at): refused while they are the only manager of an open account.
-- Memberships stay, but a closed person is refused by every account trigger below.
-- The caller-is-not-self rule stays in the API.
CREATE TRIGGER trg_person_close_not_only_manager
BEFORE UPDATE OF closed_at ON person
WHEN NEW.closed_at IS NOT NULL AND OLD.closed_at IS NULL
 AND EXISTS (SELECT 1 FROM account_role r JOIN account a ON a.id = r.account_id
             WHERE r.person_id = NEW.id AND r.role = 'manager' AND a.closed_at IS NULL
               AND NOT EXISTS (SELECT 1 FROM account_role m JOIN person p ON p.id = m.person_id
                               WHERE m.account_id = r.account_id AND m.role = 'manager'
                                 AND m.person_id <> NEW.id AND p.closed_at IS NULL))
BEGIN
    SELECT RAISE(ABORT, 'person is the only manager of an open account');
END;

CREATE TRIGGER trg_closed_person_no_session
BEFORE INSERT ON session
WHEN (SELECT closed_at FROM person WHERE id = NEW.person_id) IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'person is closed');
END;

CREATE TRIGGER trg_closed_person_no_role
BEFORE INSERT ON account_role
WHEN (SELECT closed_at FROM person WHERE id = NEW.person_id) IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'person is closed');
END;

-- An open account always keeps one open manager. Creating an account with its first manager is the API's job
-- (a trigger can't know more members are coming), and the API must diff the member list on update (insert or
-- change first, delete last) because these fire row by row. Closed accounts are exempt.
CREATE TRIGGER trg_last_manager_not_removed
BEFORE DELETE ON account_role
WHEN OLD.role = 'manager'
 AND (SELECT closed_at FROM account WHERE id = OLD.account_id) IS NULL
 AND NOT EXISTS (SELECT 1 FROM account_role m JOIN person p ON p.id = m.person_id
                 WHERE m.account_id = OLD.account_id AND m.role = 'manager'
                   AND m.person_id <> OLD.person_id AND p.closed_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'account needs at least one manager');
END;

CREATE TRIGGER trg_last_manager_not_demoted
BEFORE UPDATE OF role ON account_role
WHEN OLD.role = 'manager' AND NEW.role <> 'manager'
 AND (SELECT closed_at FROM account WHERE id = OLD.account_id) IS NULL
 AND NOT EXISTS (SELECT 1 FROM account_role m JOIN person p ON p.id = m.person_id
                 WHERE m.account_id = OLD.account_id AND m.role = 'manager'
                   AND m.person_id <> OLD.person_id AND p.closed_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'account needs at least one manager');
END;

-- A closed account, or an account of a closed family, takes no new deposits, withdrawals (this includes approvals
-- and funding), requests or skips. Closing the family needs no other cleanup: nothing else can change money.
CREATE TRIGGER trg_account_open_for_transaction
BEFORE INSERT ON account_transaction
WHEN EXISTS (SELECT 1 FROM account a JOIN family f ON f.id = a.family_id
             WHERE a.id = NEW.account_id AND (a.closed_at IS NOT NULL OR f.closed_at IS NOT NULL))
BEGIN
    SELECT RAISE(ABORT, 'account or family is closed');
END;

CREATE TRIGGER trg_account_open_for_request
BEFORE INSERT ON transaction_request
WHEN EXISTS (SELECT 1 FROM account a JOIN family f ON f.id = a.family_id
             WHERE a.id = NEW.account_id AND (a.closed_at IS NOT NULL OR f.closed_at IS NOT NULL))
BEGIN
    SELECT RAISE(ABORT, 'account or family is closed');
END;

CREATE TRIGGER trg_account_open_for_skip
BEFORE INSERT ON funding_skip
WHEN EXISTS (SELECT 1 FROM account a JOIN family f ON f.id = a.family_id
             WHERE a.id = NEW.account_id AND (a.closed_at IS NOT NULL OR f.closed_at IS NOT NULL))
BEGIN
    SELECT RAISE(ABORT, 'account or family is closed');
END;

-- Members of a closed family can't log in (the API also revokes their sessions when the family is closed).
CREATE TRIGGER trg_closed_family_no_session
BEFORE INSERT ON session
WHEN EXISTS (SELECT 1 FROM person p JOIN family f ON f.id = p.family_id
             WHERE p.id = NEW.person_id AND f.closed_at IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'family is closed');
END;

-- Direct transactions: a manager may deposit and withdraw; a user may only withdraw. Closed persons can't.
-- Approved-request deposits and the automatic daily funding (no person) are authorized elsewhere.
CREATE TRIGGER trg_transaction_authorized
BEFORE INSERT ON account_transaction
WHEN NEW.request_id IS NULL AND NEW.funding_date IS NULL
 AND NOT EXISTS (
     SELECT 1 FROM account_role r JOIN person p ON p.id = r.person_id
     WHERE r.account_id = NEW.account_id AND r.person_id = NEW.person_id AND p.closed_at IS NULL
       AND (r.role = 'manager' OR NEW.type = 'withdrawal'))
BEGIN
    SELECT RAISE(ABORT, 'not authorized on account');
END;

CREATE TRIGGER trg_funding_skip_by_manager
BEFORE INSERT ON funding_skip
WHEN NOT EXISTS (SELECT 1 FROM account_role r JOIN person p ON p.id = r.person_id
                 WHERE r.account_id = NEW.account_id AND r.person_id = NEW.created_by AND r.role = 'manager' AND p.closed_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'not authorized on account');
END;

CREATE TRIGGER trg_request_by_member
BEFORE INSERT ON transaction_request
WHEN NOT EXISTS (SELECT 1 FROM account_role r JOIN person p ON p.id = r.person_id
                 WHERE r.account_id = NEW.account_id AND r.person_id = NEW.requested_by AND p.closed_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'not authorized on account');
END;

CREATE TRIGGER trg_request_decided_by_manager
BEFORE UPDATE OF status ON transaction_request
WHEN NEW.status <> 'pending'
 AND NOT EXISTS (SELECT 1 FROM account_role r JOIN person p ON p.id = r.person_id
                 WHERE r.account_id = NEW.account_id AND r.person_id = NEW.decided_by AND r.role = 'manager' AND p.closed_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'not authorized on account');
END;

-- Withdrawals can only spend posted money, never pending requests.
CREATE TRIGGER trg_no_overdraft
BEFORE INSERT ON account_transaction
WHEN NEW.type = 'withdrawal'
 AND COALESCE((SELECT balance FROM account_balance WHERE account_id = NEW.account_id), 0) < NEW.amount
BEGIN
    SELECT RAISE(ABORT, 'insufficient funds');
END;

CREATE TRIGGER trg_request_decided_once
BEFORE UPDATE OF status ON transaction_request
WHEN OLD.status <> 'pending'
BEGIN
    SELECT RAISE(ABORT, 'request already decided');
END;

-- The ledger is append-only: a mistake is corrected by a new transaction, never an edit. A request keeps
-- everything the requester sent; only its decision columns change, and it is never deleted.
CREATE TRIGGER trg_transaction_immutable_upd
BEFORE UPDATE ON account_transaction
BEGIN
    SELECT RAISE(ABORT, 'transactions are immutable');
END;

CREATE TRIGGER trg_transaction_immutable_del
BEFORE DELETE ON account_transaction
BEGIN
    SELECT RAISE(ABORT, 'transactions are immutable');
END;

CREATE TRIGGER trg_request_immutable_upd
BEFORE UPDATE OF id, account_id, amount, requested_by, requested_at, requester_comment, idempotency_key ON transaction_request
BEGIN
    SELECT RAISE(ABORT, 'requests are immutable');
END;

CREATE TRIGGER trg_request_immutable_del
BEFORE DELETE ON transaction_request
BEGIN
    SELECT RAISE(ABORT, 'requests are immutable');
END;

-- A deposit that cites a request must match that approved request (the only way around trg_transaction_authorized).
CREATE TRIGGER trg_transaction_matches_request
BEFORE INSERT ON account_transaction
WHEN NEW.request_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM transaction_request r
                 WHERE r.id = NEW.request_id AND r.status = 'approved'
                   AND r.account_id = NEW.account_id AND r.amount = NEW.amount)
BEGIN
    SELECT RAISE(ABORT, 'request is not approved for this deposit');
END;

-- A family always keeps one open admin (is_admin), or nobody could manage it.
CREATE TRIGGER trg_last_admin_kept
BEFORE UPDATE OF closed_at, is_admin ON person
WHEN OLD.is_admin = 1 AND OLD.closed_at IS NULL AND (NEW.closed_at IS NOT NULL OR NEW.is_admin = 0)
 AND NOT EXISTS (SELECT 1 FROM person p WHERE p.family_id = OLD.family_id AND p.id <> OLD.id
                 AND p.is_admin = 1 AND p.closed_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'family needs at least one admin');
END;

-- Approval posts the deposit atomically with the status change.
CREATE TRIGGER trg_request_approved
AFTER UPDATE OF status ON transaction_request
WHEN NEW.status = 'approved'
BEGIN
    INSERT INTO account_transaction (account_id, person_id, type, amount, request_id, comment)
    VALUES (NEW.account_id, NEW.decided_by, 'deposit', NEW.amount, NEW.id, NEW.requester_comment);
END;
