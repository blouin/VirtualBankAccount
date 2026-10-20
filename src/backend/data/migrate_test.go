package data

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

const dbDir = "../../../db"

func open(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	d, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	return d, path
}

func mustExec(t *testing.T, d *sql.DB, q string) {
	t.Helper()
	if _, err := d.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestMigrate(t *testing.T) {
	ctx := context.Background()
	d, path := open(t)
	m := fstest.MapFS{}
	files, _ := fs.Glob(os.DirFS(dbDir), "*.sql")
	for _, f := range files {
		b, _ := os.ReadFile(filepath.Join(dbDir, f))
		m[f] = &fstest.MapFile{Data: b}
	}
	n := len(m)

	for i := 0; i < 2; i++ { // fresh build, then re-run is a no-op
		if v, err := Migrate(ctx, d, m, path); err != nil || v != n {
			t.Fatal(v, err)
		}
	}

	m[fmt.Sprintf("%03d_test.sql", n+1)] = &fstest.MapFile{Data: []byte("ALTER TABLE person ADD COLUMN nickname TEXT;")}
	if v, err := Migrate(ctx, d, m, path); err != nil || v != n+1 {
		t.Fatal(v, err)
	}
	if _, err := os.Stat(fmt.Sprintf("%s.v%d.bak", path, n)); err != nil {
		t.Fatal("no backup:", err)
	}
	mustExec(t, d, "SELECT nickname FROM person")

	m[fmt.Sprintf("%03d_bad.sql", n+2)] = &fstest.MapFile{Data: []byte("CREATE TABLE x (id INTEGER);\nSELECT * FROM missing;")}
	if _, err := Migrate(ctx, d, m, path); err == nil {
		t.Fatal("bad migration applied")
	}
	var v, x int
	d.QueryRow("PRAGMA user_version").Scan(&v)
	d.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='x'").Scan(&x)
	if v != n+1 || x != 0 {
		t.Fatalf("not rolled back: version=%d x=%d", v, x)
	}
}

func TestMigrateRefusesNewerDatabase(t *testing.T) {
	d, path := open(t)
	mustExec(t, d, "PRAGMA user_version = 99")
	if _, err := Migrate(context.Background(), d, os.DirFS(dbDir), path); err == nil {
		t.Fatal("older code accepted a newer schema")
	}
}

func TestClosedFamily(t *testing.T) {
	ctx := context.Background()
	d, path := open(t)
	if _, err := Migrate(ctx, d, os.DirFS(dbDir), path); err != nil {
		t.Fatal(err)
	}
	mustExec(t, d, `
INSERT INTO family (id, name, code) VALUES (1, 'A', 'a'), (2, 'B', 'b');
INSERT INTO person (id, family_id, name, password_hash, is_admin) VALUES (1, 1, 'mom', 'x', 1), (2, 2, 'dad', 'x', 1);
INSERT INTO account (id, family_id, name, daily_funding) VALUES (1, 1, 'A acct', 500), (2, 2, 'B acct', 500);
INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 1, 1, 'manager'), (2, 2, 2, 'manager');
INSERT INTO account_transaction (account_id, person_id, type, amount) VALUES (1, 1, 'deposit', 1000), (2, 2, 'deposit', 1000);
INSERT INTO session (token_hash, person_id, expires_at) VALUES ('t', 1, datetime('now', '+1 day'));`)
	fails := func(q, msg string) {
		t.Helper()
		if _, err := d.Exec(q); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s\n  want error containing %q, got %v", q, msg, err)
		}
	}
	tx := "INSERT INTO account_transaction (account_id, person_id, type, amount) VALUES "

	mustExec(t, d, "UPDATE family SET closed_at=datetime('now') WHERE id=1")
	fails("INSERT INTO session (token_hash, person_id, expires_at) VALUES ('u', 1, datetime('now', '+1 day'))", "family is closed")
	fails(tx+"(1, 1, 'deposit', 1)", "family is closed")
	fails("INSERT INTO transaction_request (account_id, amount, requested_by) VALUES (1, 1, 1)", "family is closed")
	fails("INSERT INTO funding_skip (account_id, skip_date, created_by) VALUES (1, '2030-01-01', 1)", "family is closed")
	if n, err := FundDaily(ctx, d, 1, "2026-10-19"); err != nil || n != 0 {
		t.Errorf("closed family funded %d, %v", n, err)
	}

	// the other family is untouched, and the closed one's data stays readable
	mustExec(t, d, tx+"(2, 2, 'withdrawal', 100)")
	if n, err := FundDaily(ctx, d, 2, "2026-10-19"); err != nil || n != 1 {
		t.Errorf("open family funded %d, %v", n, err)
	}
	var b int
	if err := d.QueryRow("SELECT balance FROM account_balance WHERE account_id = 1").Scan(&b); err != nil || b != 1000 {
		t.Errorf("closed family balance = %d, %v", b, err)
	}

	// reopening restores everything
	mustExec(t, d, "UPDATE family SET closed_at=NULL WHERE id=1")
	mustExec(t, d, tx+"(1, 1, 'deposit', 1)")
	mustExec(t, d, "INSERT INTO session (token_hash, person_id, expires_at) VALUES ('v', 1, NULL)") // remember = true: no expiry
	fails("INSERT INTO session (token_hash, person_id) VALUES ('v', 1)", "UNIQUE")
	var live int
	d.QueryRow("SELECT count(*) FROM session WHERE expires_at IS NULL OR expires_at > datetime('now')").Scan(&live)
	if live != 2 {
		t.Errorf("valid sessions = %d, want 2 (one dated, one without expiry)", live)
	}
}

func TestRules(t *testing.T) {
	d, path := open(t)
	if _, err := Migrate(context.Background(), d, os.DirFS(dbDir), path); err != nil {
		t.Fatal(err)
	}
	mustExec(t, d, `
INSERT INTO family (id, name, code) VALUES (1, 'A', 'a'), (2, 'B', 'b');
INSERT INTO person (id, family_id, name, password_hash, is_admin) VALUES (1, 1, 'mom', 'x', 0), (2, 1, 'kid', 'x', 0), (3, 1, 'family admin', 'y', 1), (4, 2, 'mom', 'x', 0), (5, 1, 'second admin', 'y', 1);
INSERT INTO account (id, family_id, name) VALUES (1, 1, 'Kid savings');
INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 1, 1, 'manager'), (1, 2, 1, 'user');
INSERT INTO account_transaction (account_id, person_id, type, amount) VALUES (1, 1, 'deposit', 1000);
INSERT INTO transaction_request (id, account_id, amount, requested_by) VALUES (1, 1, 500, 2), (2, 1, 300, 2);`)

	bal := func(want [3]int) {
		t.Helper()
		var b [3]int
		if err := d.QueryRow("SELECT balance, pending, total FROM account_balance").Scan(&b[0], &b[1], &b[2]); err != nil || b != want {
			t.Fatalf("balance %v, want %v (%v)", b, want, err)
		}
	}
	fails := func(q, msg string) {
		t.Helper()
		if _, err := d.Exec(q); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s\n  want error containing %q, got %v", q, msg, err)
		}
	}
	tx := "INSERT INTO account_transaction (account_id, person_id, type, amount) VALUES "

	bal([3]int{1000, 800, 1800})
	fails(tx+"(1, 1, 'deposit', -5)", "CHECK constraint")
	fails(tx+"(1, 1, 'refund', 5)", "CHECK constraint")

	// everyone has a password
	fails("INSERT INTO person (family_id, name) VALUES (1, 'no pw')", "NOT NULL constraint")
	fails("UPDATE person SET password_hash=NULL WHERE id=3", "NOT NULL constraint")

	// tenancy: same name fine in another family, duplicate inside one is not; no cross-family membership
	fails("INSERT INTO person (family_id, name, password_hash) VALUES (1, 'kid', 'x')", "UNIQUE constraint")
	fails("INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 4, 1, 'user')", "FOREIGN KEY")
	fails("INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 4, 2, 'user')", "FOREIGN KEY")
	fails("INSERT INTO login_attempt (person_id, operator_id, ip, succeeded) VALUES (1, 1, 'x', 0)", "CHECK constraint")

	// account-level authorization (family admin is not a member, so no account rights)
	fails(tx+"(1, 2, 'deposit', 100)", "not authorized") // user deposit
	fails(tx+"(1, 3, 'deposit', 100)", "not authorized")
	fails(tx+"(1, 3, 'withdrawal', 1)", "not authorized")
	fails("INSERT INTO transaction_request (account_id, amount, requested_by) VALUES (1, 1, 3)", "not authorized")
	fails("UPDATE transaction_request SET status='approved', decided_by=3 WHERE id=1", "not authorized")
	fails("UPDATE transaction_request SET status='approved', decided_by=2 WHERE id=1", "not authorized") // user can't approve
	fails("INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 2, 1, 'admin')", "CHECK constraint")

	fails(tx+"(1, 2, 'withdrawal', 1001)", "insufficient funds")

	mustExec(t, d, "UPDATE transaction_request SET status='approved', decided_by=1, decided_at=datetime('now') WHERE id=1")
	mustExec(t, d, "UPDATE transaction_request SET status='rejected', decided_by=1, decided_at=datetime('now') WHERE id=2")
	bal([3]int{1500, 0, 1500})
	var typ string
	var amount, person int
	d.QueryRow("SELECT type, amount, person_id FROM account_transaction WHERE request_id = 1").Scan(&typ, &amount, &person)
	if typ != "deposit" || amount != 500 || person != 1 {
		t.Errorf("approval deposit = %s %d %d", typ, amount, person)
	}
	fails("UPDATE transaction_request SET status='approved' WHERE id=2", "already decided")

	mustExec(t, d, tx+"(1, 2, 'withdrawal', 1500)")
	bal([3]int{0, 0, 0})

	// closed accounts stay in the balance view
	var closedAt sql.NullString
	if err := d.QueryRow("SELECT closed_at FROM account_balance").Scan(&closedAt); err != nil || closedAt.Valid {
		t.Fatalf("open account closed_at = %v (%v)", closedAt, err)
	}
	// the ledger and requests are append-only; a deposit citing a request must match an approved one
	fails("UPDATE account_transaction SET amount=1 WHERE id=1", "immutable")
	fails("DELETE FROM account_transaction WHERE id=1", "immutable")
	fails("UPDATE transaction_request SET amount=1 WHERE id=1", "immutable")
	fails("DELETE FROM transaction_request WHERE id=1", "immutable")
	fails("INSERT INTO account_transaction (account_id, person_id, type, amount, request_id) VALUES (1, 1, 'deposit', 300, 2)", "not approved")
	mustExec(t, d, "INSERT INTO transaction_request (id, account_id, amount, requested_by) VALUES (3, 1, 5, 2)")
	fails("UPDATE transaction_request SET status='rejected', decided_by=1 WHERE id=3", "CHECK constraint") // decided_at missing
	// login rate limiting window
	mustExec(t, d, `
INSERT INTO login_attempt (person_id, ip, succeeded) VALUES (1, '1.1.1.1', 0), (1, '1.1.1.1', 0), (1, '2.2.2.2', 1);
INSERT INTO login_attempt (person_id, ip, succeeded, attempted_at) VALUES (1, '1.1.1.1', 0, datetime('now', '-16 minutes'));`)
	rows, err := d.Query("SELECT kind, key, failures FROM login_failures_15m")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for rows.Next() {
		var kind, key string
		var n int
		rows.Scan(&kind, &key, &n)
		got[kind+"/"+key] = n
	}
	rows.Close()
	if len(got) != 2 || got["person/1"] != 2 || got["ip/1.1.1.1"] != 2 {
		t.Errorf("login failures = %v", got)
	}

	// closing a person: refused for the only manager of an open account; fine once another open manager exists.
	// Memberships stay, but a closed person is refused everywhere.
	mustExec(t, d, "INSERT INTO session (token_hash, person_id, expires_at) VALUES ('t', 2, datetime('now', '+1 day'))")
	fails("UPDATE person SET closed_at=datetime('now') WHERE id=1", "only manager")
	mustExec(t, d, "INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 3, 1, 'manager')")
	mustExec(t, d, "UPDATE person SET closed_at=datetime('now') WHERE id=1")
	fails("UPDATE person SET closed_at=datetime('now') WHERE id=3", "only manager") // mom is closed, so 3 is the last
	fails(tx+"(1, 1, 'deposit', 1)", "not authorized")
	fails("INSERT INTO funding_skip (account_id, skip_date, created_by) VALUES (1, '2030-01-01', 1)", "not authorized")
	mustExec(t, d, "UPDATE person SET closed_at=datetime('now') WHERE id=2") // a user can always be closed
	fails("INSERT INTO session (token_hash, person_id, expires_at) VALUES ('u', 2, datetime('now', '+1 day'))", "person is closed")
	fails("INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 2, 1, 'user')", "person is closed")
	mustExec(t, d, tx+"(1, 3, 'deposit', 50)")
	fails(tx+"(1, 2, 'withdrawal', 1)", "not authorized")
	fails("INSERT INTO transaction_request (account_id, amount, requested_by) VALUES (1, 1, 2)", "not authorized")

	// the last open manager can't be removed or demoted while the account is open
	fails("DELETE FROM account_role WHERE account_id=1 AND person_id=3", "at least one manager")
	fails("UPDATE account_role SET role='user' WHERE account_id=1 AND person_id=3", "at least one manager")

	// a closed account takes nothing new (deposit, request, skip); its managers may then be removed
	mustExec(t, d, "UPDATE account SET closed_at=datetime('now') WHERE id=1")
	fails(tx+"(1, 3, 'deposit', 1)", "account or family is closed")
	fails("INSERT INTO transaction_request (account_id, amount, requested_by) VALUES (1, 1, 3)", "account or family is closed")
	fails("INSERT INTO funding_skip (account_id, skip_date, created_by) VALUES (1, '2030-01-01', 3)", "account or family is closed")
	mustExec(t, d, "DELETE FROM account_role WHERE account_id=1 AND person_id=3")
	mustExec(t, d, "UPDATE person SET closed_at=datetime('now') WHERE id=3")

	// a family keeps one open admin
	fails("UPDATE person SET closed_at=datetime('now') WHERE id=5", "at least one admin")
	fails("UPDATE person SET is_admin=0 WHERE id=5", "at least one admin")
}
