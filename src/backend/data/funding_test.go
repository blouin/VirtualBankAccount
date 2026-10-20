package data

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestFunding(t *testing.T) {
	ctx := context.Background()
	d, path := open(t)
	if _, err := Migrate(ctx, d, os.DirFS(dbDir), path); err != nil {
		t.Fatal(err)
	}
	mustExec(t, d, `
INSERT INTO family (id, name, code) VALUES (1, 'F', 'f');
INSERT INTO person (id, family_id, name, password_hash) VALUES (1, 1, 'mom', 'x'), (2, 1, 'kid', 'x');
INSERT INTO account (id, family_id, name, daily_funding, closed_at) VALUES
  (1, 1, 'funded',        500, NULL),
  (2, 1, 'skips today',   500, NULL),
  (3, 1, 'funding off',     0, NULL),
  (4, 1, 'closed',        500, datetime('now')),
  (5, 1, 'skips tomorrow', 500, NULL);
INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 1, 1, 'manager'), (2, 1, 1, 'manager'), (5, 1, 1, 'manager'), (5, 2, 1, 'user');
INSERT INTO funding_skip (account_id, skip_date, created_by) VALUES (2, '2026-10-19', 1), (5, '2026-10-20', 1);`)

	fund := func(date string, want int64) {
		t.Helper()
		if n, err := FundDaily(ctx, d, 1, date); err != nil || n != want {
			t.Fatalf("FundDaily(%s) = %d, %v; want %d", date, n, err, want)
		}
	}
	balance := func(id, want int) {
		t.Helper()
		var b int
		if err := d.QueryRow("SELECT balance FROM account_balance WHERE account_id = ?", id).Scan(&b); err != nil || b != want {
			t.Fatalf("account %d balance = %d, %v; want %d", id, b, err, want)
		}
	}
	fails := func(q, msg string) {
		t.Helper()
		if _, err := d.Exec(q); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s\n  want error containing %q, got %v", q, msg, err)
		}
	}

	fund("2026-10-19", 2) // accounts 1 and 5; 2 skipped, 3 off, 4 closed
	fund("2026-10-19", 0) // re-run does nothing
	balance(1, 500)
	balance(2, 0)
	balance(5, 500)

	fund("2026-10-20", 2) // accounts 1 and 2; 5 skips this day, the earlier skip on 2 has expired
	balance(1, 1000)
	balance(2, 500)
	balance(5, 500)

	// funding rows carry no person, and nobody else can create person-less or dated rows
	var person *int
	if err := d.QueryRow("SELECT person_id FROM account_transaction WHERE funding_date = '2026-10-19' AND account_id = 1").Scan(&person); err != nil || person != nil {
		t.Errorf("funding person_id = %v, %v; want NULL", person, err)
	}
	tx := "INSERT INTO account_transaction (account_id, person_id, type, amount, funding_date) VALUES "
	fails(tx+"(1, NULL, 'deposit', 5, NULL)", "not authorized")        // no person and no funding day
	fails(tx+"(1, 1, 'deposit', 5, '2026-10-21')", "CHECK constraint") // funding day with a person
	fails(tx+"(1, NULL, 'withdrawal', 5, '2026-10-21')", "CHECK constraint")
	fails(tx+"(1, NULL, 'deposit', 5, '2026-02-30')", "CHECK constraint")  // not a real date
	fails(tx+"(1, NULL, 'deposit', 5, '2026-10-19')", "UNIQUE constraint") // once per account per day

	// only a manager of the account can skip a day, with a real date
	skip := "INSERT INTO funding_skip (account_id, skip_date, created_by) VALUES "
	fails(skip+"(5, '2026-10-22', 2)", "not authorized") // user
	fails(skip+"(3, '2026-10-22', 1)", "not authorized") // not a member
	fails(skip+"(5, '2026-10-22 ', 1)", "CHECK constraint")
	fails("UPDATE account SET daily_funding = -1 WHERE id = 1", "CHECK constraint")
	long := strings.Repeat("x", 201)
	fails("INSERT INTO funding_skip (account_id, skip_date, created_by, comment) VALUES (5, '2026-10-23', 1, '"+long+"')", "CHECK constraint")
	fails("INSERT INTO transaction_request (account_id, amount, requested_by, requester_comment) VALUES (5, 5, 2, '"+long+"')", "CHECK constraint")
	fails("INSERT INTO transaction_request (account_id, amount, requested_by, decider_comment) VALUES (5, 5, 2, 'x')", "CHECK constraint") // no decision yet
	mustExec(t, d, "INSERT INTO funding_skip (account_id, skip_date, created_by, comment) VALUES (5, '2026-10-23', 1, 'trip')")
}
