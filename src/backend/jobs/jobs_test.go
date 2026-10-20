package jobs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"vbank/data"
)

func TestDueDate(t *testing.T) {
	for _, c := range []struct {
		at, tz, date string
		due          bool
	}{
		{"2026-10-20T03:59:00Z", "America/Toronto", "2026-10-19", true},  // 23:59 EDT
		{"2026-12-16T04:59:00Z", "America/Toronto", "2026-12-15", true},  // 23:59 EST
		{"2026-10-20T04:59:00Z", "America/Toronto", "2026-10-20", false}, // 00:59
		{"2026-10-19T14:59:00Z", "Asia/Tokyo", "2026-10-19", true},       // 23:59 JST
	} {
		at, _ := time.Parse(time.RFC3339, c.at)
		if date, due, err := dueDate(at, c.tz); err != nil || date != c.date || due != c.due {
			t.Errorf("dueDate(%s, %s) = %s, %v, %v; want %s, %v", c.at, c.tz, date, due, err, c.date, c.due)
		}
	}
	if _, _, err := dueDate(time.Now(), "Nope/Zone"); err == nil {
		t.Error("bad zone accepted")
	}
}

func TestCreateOperator(t *testing.T) {
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := Event{Task: "create-operator", Name: "root", Password: "correct horse battery"}
	if _, err := Run(ctx, db, e); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := db.QueryRow("SELECT password_hash FROM operator WHERE name = 'root'").Scan(&hash); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(e.Password)) != nil {
		t.Fatalf("stored hash wrong: %v", err)
	}
	if _, err := Run(ctx, db, e); err == nil {
		t.Error("duplicate operator accepted")
	}
	e.Name, e.Password = "x", "short"
	if _, err := Run(ctx, db, e); err == nil {
		t.Error("short password accepted")
	}
}

func TestResetOperatorPassword(t *testing.T) {
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := Run(ctx, db, Event{Task: "create-operator", Name: "root", Password: "correct horse battery"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO operator_session (token, operator_id, expires_at) VALUES ('t', 1, datetime('now', '+1 hour'))"); err != nil {
		t.Fatal(err)
	}
	e := Event{Task: "reset-operator-password", Name: "root", Password: "a brand new password"}
	if _, err := Run(ctx, db, e); err != nil {
		t.Fatal(err)
	}
	var hash string
	var sessions int
	db.QueryRow("SELECT password_hash FROM operator WHERE name = 'root'").Scan(&hash)
	db.QueryRow("SELECT count(*) FROM operator_session").Scan(&sessions)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(e.Password)) != nil || sessions != 0 {
		t.Errorf("password not reset or sessions kept (%d)", sessions)
	}
	if _, err := Run(ctx, db, Event{Task: "reset-operator-password", Name: "nobody", Password: e.Password}); err == nil {
		t.Error("unknown operator accepted")
	}
	if _, err := Run(ctx, db, Event{Task: "reset-operator-password", Name: "root", Password: "short"}); err == nil {
		t.Error("short password accepted")
	}
}

func TestSeedPOC(t *testing.T) {
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := Event{Task: "seed-poc", Family: &SeedFamily{Code: "POC", Name: "POC Family", DailyFunding: 50}, People: []SeedPerson{
		{Name: "Parent 1", Password: "parent1-pass1", IsAdmin: true}, {Name: "Parent 2", Password: "parent2-pass1", IsAdmin: true},
		{Name: "Child 1", Password: "child1-pass1"}, {Name: "Child 2", Password: "child2-pass1"}}}
	if _, err := Run(ctx, db, e); err != nil {
		t.Fatal(err)
	}
	counts := func() (s string) {
		db.QueryRow(`SELECT (SELECT count(*) FROM family) || '/' || (SELECT count(*) FROM person) || '/' ||
			(SELECT count(*) FROM account) || '/' || (SELECT count(*) FROM account_role) || '/' ||
			(SELECT count(*) FROM account WHERE daily_funding = 50) || '/' || (SELECT count(*) FROM person WHERE is_admin)`).Scan(&s)
		return
	}
	if got := counts(); got != "1/4/2/6/2/2" {
		t.Errorf("family/persons/accounts/roles/funded/admins = %s", got)
	}
	var roles string
	db.QueryRow(`SELECT group_concat(a.name || ':' || p.name || '=' || ar.role, ' ') FROM account_role ar
		JOIN account a ON a.id = ar.account_id JOIN person p ON p.id = ar.person_id ORDER BY 1`).Scan(&roles)
	t.Log(roles)
	var tz string
	db.QueryRow("SELECT timezone FROM family WHERE code = 'POC'").Scan(&tz)
	if tz != "America/Toronto" {
		t.Errorf("timezone %s", tz)
	}
	passwordOK := func(name, pw string) bool {
		var hash string
		db.QueryRow("SELECT password_hash FROM person WHERE name = ?", name).Scan(&hash)
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
	}
	if !passwordOK("Parent 1", "parent1-pass1") || !passwordOK("Child 2", "child2-pass1") {
		t.Error("passwords not stored")
	}
	var childRole, parentRole string
	db.QueryRow("SELECT role FROM account_role WHERE person_id = (SELECT id FROM person WHERE name = 'Child 1') AND account_id = (SELECT id FROM account WHERE name = 'Child 1')").Scan(&childRole)
	db.QueryRow("SELECT role FROM account_role WHERE person_id = (SELECT id FROM person WHERE name = 'Parent 1') AND account_id = (SELECT id FROM account WHERE name = 'Child 2')").Scan(&parentRole)
	if childRole != "user" || parentRole != "manager" {
		t.Errorf("roles: Child 1 %q, Parent 1 %q", childRole, parentRole)
	}

	// a rerun updates: new password, tampered funding and role are restored, sessions end, nothing is duplicated
	db.Exec("INSERT INTO session (token_hash, person_id) VALUES ('t', (SELECT id FROM person WHERE name = 'Child 1'))")
	db.Exec("UPDATE account SET daily_funding = 0")
	db.Exec("UPDATE account_role SET role = 'manager' WHERE person_id = (SELECT id FROM person WHERE name = 'Child 1')")
	e.People[2].Password = "child1-new-pass"
	if _, err := Run(ctx, db, e); err != nil {
		t.Fatal(err)
	}
	var sessions int
	db.QueryRow("SELECT count(*) FROM session").Scan(&sessions)
	if got := counts(); got != "1/4/2/6/2/2" || !passwordOK("Child 1", "child1-new-pass") || passwordOK("Child 1", "child1-pass1") || sessions != 0 {
		t.Errorf("rerun: counts %s, sessions %d", got, sessions)
	}
	db.QueryRow("SELECT role FROM account_role WHERE person_id = (SELECT id FROM person WHERE name = 'Child 1') AND account_id = (SELECT id FROM account WHERE name = 'Child 1')").Scan(&childRole)
	if childRole != "user" {
		t.Errorf("Child 1's role after rerun = %q", childRole)
	}

	one := func(f *SeedFamily, p ...SeedPerson) Event { return Event{Task: "seed-poc", Family: f, People: p} }
	fam := &SeedFamily{Code: "X", Name: "X"}
	admin, kid := SeedPerson{Name: "A", Password: "password1", IsAdmin: true}, SeedPerson{Name: "K", Password: "password1"}
	for name, bad := range map[string]Event{
		"no family":      one(nil, admin),
		"no code":        one(&SeedFamily{Name: "X"}, admin),
		"no name":        one(&SeedFamily{Code: "X"}, admin),
		"bad timezone":   one(&SeedFamily{Code: "X", Name: "X", Timezone: "Nope/Zone"}, admin),
		"negative fund":  one(&SeedFamily{Code: "X", Name: "X", DailyFunding: -1}, admin),
		"no admin":       one(fam, kid),
		"no people":      one(fam),
		"duplicate name": one(fam, admin, admin),
		"nameless":       one(fam, admin, SeedPerson{Password: "password1"}),
		"short password": one(fam, admin, SeedPerson{Name: "K", Password: "short"}),
		"long password":  one(fam, SeedPerson{Name: "A", Password: strings.Repeat("x", 73), IsAdmin: true}),
	} {
		if _, err := Run(ctx, db, bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if got := counts(); got != "1/4/2/6/2/2" {
		t.Errorf("rejected seeds changed the data: %s", got)
	}

	// optional fields default: Toronto, no funding
	if _, err := Run(ctx, db, one(&SeedFamily{Code: "OTHER", Name: "Other"}, admin, kid)); err != nil {
		t.Fatal(err)
	}
	var otherTZ string
	var otherFund int
	db.QueryRow("SELECT f.timezone, a.daily_funding FROM family f JOIN account a ON a.family_id = f.id WHERE f.code = 'OTHER'").Scan(&otherTZ, &otherFund)
	if otherTZ != "America/Toronto" || otherFund != 0 {
		t.Errorf("defaults: %s, %d", otherTZ, otherFund)
	}
	db.Exec("DELETE FROM account_role WHERE family_id = 2; DELETE FROM account WHERE family_id = 2; DELETE FROM person WHERE family_id = 2; DELETE FROM family WHERE id = 2")
	// a closed family is not funded
	db.Exec("UPDATE family SET closed_at = datetime('now')")
	if res, err := Run(ctx, db, Event{Task: "daily-funding", ScheduledTime: "2026-10-20T03:59:00Z"}); err != nil || res.(map[string]any)["funded"] != int64(0) {
		t.Errorf("closed family funding = %v, %v", res, err)
	}
}

func TestRevokeSessions(t *testing.T) {
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
INSERT INTO family (id, name, code) VALUES (1, 'A', 'aaaa'), (2, 'B', 'bbbb');
INSERT INTO person (id, family_id, name, password_hash) VALUES (1, 1, 'mom', 'x'), (2, 1, 'kid', 'x'), (3, 2, 'other', 'x');
INSERT INTO session (token_hash, person_id) VALUES ('a', 1), ('b', 2), ('c', 2), ('d', 3);`); err != nil {
		t.Fatal(err)
	}
	count := func() (n int) { db.QueryRow("SELECT count(*) FROM session").Scan(&n); return }
	if res, err := Run(ctx, db, Event{Task: "revoke-sessions", PersonID: 2}); err != nil || res.(map[string]any)["revoked"] != int64(2) || count() != 2 {
		t.Fatalf("person revoke = %v, %v, left %d", res, err, count())
	}
	if _, err := Run(ctx, db, Event{Task: "revoke-sessions", FamilyCode: "aaaa"}); err != nil || count() != 1 {
		t.Fatalf("family revoke: %v, left %d (family B must keep its session)", err, count())
	}
	if _, err := Run(ctx, db, Event{Task: "revoke-sessions"}); err == nil {
		t.Error("no target accepted")
	}
	if _, err := Run(ctx, db, Event{Task: "revoke-sessions", PersonID: 1, FamilyCode: "aaaa"}); err == nil {
		t.Error("both targets accepted")
	}
}

func TestRunForce(t *testing.T) {
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`
INSERT INTO family (id, name, code) VALUES (1, 'F', 'f');
INSERT INTO account (id, family_id, name, daily_funding) VALUES (1, 1, 'a', 100);`); err != nil {
		t.Fatal(err)
	}
	at := "2026-10-20T14:00:00Z" // Toronto 10:00, outside the 23:00 hour
	for i, want := range []int64{0, 1, 0} {
		force := i > 0
		res, err := Run(ctx, db, Event{Task: "daily-funding", ScheduledTime: at, Force: force})
		if m, _ := res.(map[string]any); err != nil || m["funded"] != want {
			t.Fatalf("run %d (force=%v) = %v, %v; want funded %d", i, force, res, err, want)
		}
	}
}

func TestRun(t *testing.T) {
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`
INSERT INTO family (id, name, code) VALUES (1, 'F', 'f');
INSERT INTO family (id, name, code, timezone) VALUES (2, 'Tokyo', 't', 'Asia/Tokyo');
INSERT INTO person (id, family_id, name, password_hash) VALUES (1, 1, 'mom', 'x');
INSERT INTO account (id, family_id, name, daily_funding) VALUES (1, 1, 'a', 100), (2, 1, 'skipped', 100), (3, 1, 'off', 0), (4, 2, 'tokyo', 100);
INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 1, 1, 'manager'), (2, 1, 1, 'manager');
INSERT INTO funding_skip (account_id, skip_date, created_by) VALUES (2, '2026-10-19', 1);`); err != nil {
		t.Fatal(err)
	}

	run := func(at string, want int64) {
		t.Helper()
		res, err := Run(ctx, db, Event{Task: "daily-funding", ScheduledTime: at})
		m, _ := res.(map[string]any)
		if err != nil || m["funded"] != want {
			t.Fatalf("run %s = %v, %v; want funded %d", at, res, err, want)
		}
	}
	run("2026-10-20T03:59:00Z", 1) // Toronto 23:59; Tokyo is at noon
	run("2026-10-20T03:59:00Z", 0) // retry funds nothing
	run("2026-10-19T14:59:00Z", 1) // Tokyo 23:59; Toronto is at 10:59
	run("2026-10-19T20:59:00Z", 0) // nobody is at 23:xx

	db.Exec("INSERT INTO family (id, name, code, timezone) VALUES (3, 'Bad', 'b', 'Nope/Zone')")
	if res, err := Run(ctx, db, Event{Task: "daily-funding", ScheduledTime: "2026-10-21T03:59:00Z"}); err == nil || res.(map[string]any)["funded"] != int64(2) {
		t.Errorf("bad zone: %v, %v; want an error and the other families still funded", res, err)
	}
	if _, err = Run(ctx, db, Event{Task: "nope"}); err == nil {
		t.Error("unknown task accepted")
	}
}
