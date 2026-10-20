// Package jobs runs the scheduled tasks that EventBridge Scheduler triggers (see iac/main.tf).
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // bundle the zone database; the Lambda image's may be missing

	"golang.org/x/crypto/bcrypt"

	"vbank/data"
)

// Event is the scheduler payload. A Function URL event has no top-level "task" key, and web callers
// only control its body, so a web request can't trigger a task.
type Event struct {
	Task          string       `json:"task"`
	ScheduledTime string       `json:"scheduledTime"` // UTC, RFC 3339
	Name          string       `json:"name"`          // create-operator, reset-operator-password
	Password      string       `json:"password"`      // create-operator, reset-operator-password
	Family        *SeedFamily  `json:"family"`        // seed-poc
	People        []SeedPerson `json:"people"`        // seed-poc
	PersonID      int64        `json:"personId"`      // revoke-sessions: one person
	FamilyCode    string       `json:"familyCode"`    // revoke-sessions: everyone in a family
	Force         bool         `json:"force"`         // daily-funding: fund the local day of scheduledTime (default now) even outside the 23:00 hour
}

// SeedFamily and SeedPerson are the seed-poc payload. Every admin manages every account; each non-admin
// gets one account named after them, on which they are the user.
type SeedFamily struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Timezone     string `json:"timezone"`     // optional IANA name, default America/Toronto
	DailyFunding int64  `json:"dailyFunding"` // optional, cents a day on each account, default 0 (off)
}

type SeedPerson struct {
	Name     string `json:"name"`
	Password string `json:"password"` // 6-72 bytes
	IsAdmin  bool   `json:"isAdmin"`
}

// Run executes the task. The scheduler fires hourly at :59; each family whose local clock is then in the
// 23:00 hour (its timezone column) gets that local day funded. Other hours do nothing, and a retry is harmless.
func Run(ctx context.Context, db *sql.DB, e Event) (any, error) {
	switch e.Task {
	case "daily-funding":
		return fundDaily(ctx, db, e)
	case "create-operator":
		return createOperator(ctx, db, e)
	case "reset-operator-password":
		return resetOperatorPassword(ctx, db, e)
	case "seed-poc":
		return seedPOC(ctx, db, e)
	case "revoke-sessions":
		return revokeSessions(ctx, db, e)
	}
	return nil, fmt.Errorf("unknown task %q", e.Task)
}

// revokeSessions ends login sessions by hand: one person's (personId) or a whole family's (familyCode).
// Sessions without an expiry last until this or the API revokes them, so this is the lost-device recovery
// until admin_family.yaml exists. Exactly one of the two fields is required.
func revokeSessions(ctx context.Context, db *sql.DB, e Event) (any, error) {
	var res sql.Result
	var err error
	switch {
	case (e.PersonID != 0) == (e.FamilyCode != ""):
		return nil, errors.New("exactly one of personId or familyCode is required")
	case e.PersonID != 0:
		res, err = db.ExecContext(ctx, "DELETE FROM session WHERE person_id = ?", e.PersonID)
	default:
		res, err = db.ExecContext(ctx, "DELETE FROM session WHERE person_id IN (SELECT p.id FROM person p JOIN family f ON f.id = p.family_id WHERE f.code = ?)", e.FamilyCode)
	}
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	return map[string]any{"revoked": n}, nil
}

// seedPOC creates or updates one family, its people, accounts and roles in a single transaction, standing in
// for admin_family.yaml and admin_operator.yaml, which the proof of concept doesn't implement.
// It is idempotent: a rerun matches the family by code, people by name and accounts by owner name, and
// updates passwords, admin flags, funding and roles to the payload's values. It ends the listed people's
// sessions, never deletes anything, and never reopens a closed family, person or account.
func seedPOC(ctx context.Context, db *sql.DB, e Event) (any, error) {
	f, people := e.Family, e.People
	if f == nil || f.Code == "" || f.Name == "" {
		return nil, errors.New("seed: family code and name are required")
	}
	tz := f.Timezone
	if tz == "" {
		tz = "America/Toronto"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, fmt.Errorf("seed: family timezone: %w", err)
	}
	if f.DailyFunding < 0 {
		return nil, errors.New("seed: family dailyFunding can't be negative")
	}
	var admins []string
	hashes := make([]string, len(people))
	seen := map[string]bool{}
	for i, p := range people {
		switch {
		case p.Name == "":
			return nil, fmt.Errorf("seed: person %d has no name", i+1)
		case seen[p.Name]:
			return nil, fmt.Errorf("seed: duplicate person %q", p.Name)
		case len(p.Password) < 6 || len(p.Password) > 72:
			return nil, fmt.Errorf("seed: password for %s must be 6-72 characters", p.Name)
		}
		seen[p.Name] = true
		if p.IsAdmin {
			admins = append(admins, p.Name)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		hashes[i] = string(h)
	}
	if len(admins) == 0 {
		return nil, errors.New("seed: at least one person needs isAdmin true (managers of the accounts)")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var familyID int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO family (name, code, timezone) VALUES (?, ?, ?)
ON CONFLICT(code) DO UPDATE SET name = excluded.name, timezone = excluded.timezone RETURNING id`,
		f.Name, f.Code, tz).Scan(&familyID)
	if err != nil {
		return nil, fmt.Errorf("seed: family: %w", err)
	}

	persons := map[string]int64{}
	for i, p := range people {
		var id int64
		err := tx.QueryRowContext(ctx, `
INSERT INTO person (family_id, name, password_hash, is_admin) VALUES (?, ?, ?, ?)
ON CONFLICT(family_id, name) DO UPDATE SET password_hash = excluded.password_hash, is_admin = excluded.is_admin RETURNING id`,
			familyID, p.Name, hashes[i], p.IsAdmin).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("seed: person %q: %w", p.Name, err)
		}
		persons[p.Name] = id
		if _, err := tx.ExecContext(ctx, "DELETE FROM session WHERE person_id = ?", id); err != nil {
			return nil, err
		}
	}

	accounts := map[string]int64{}
	for _, p := range people {
		if p.IsAdmin {
			continue
		}
		// account names aren't unique in the schema, so the seed matches on (family, name)
		var id int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM account WHERE family_id = ? AND name = ? ORDER BY id LIMIT 1", familyID, p.Name).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			err = tx.QueryRowContext(ctx, "INSERT INTO account (family_id, name, daily_funding) VALUES (?, ?, ?) RETURNING id", familyID, p.Name, f.DailyFunding).Scan(&id)
		case err == nil:
			_, err = tx.ExecContext(ctx, "UPDATE account SET daily_funding = ? WHERE id = ?", f.DailyFunding, id)
		}
		if err != nil {
			return nil, fmt.Errorf("seed: account %q: %w", p.Name, err)
		}
		accounts[p.Name] = id
		roles := map[string]string{p.Name: "user"}
		for _, a := range admins {
			roles[a] = "manager"
		}
		for name, role := range roles {
			_, err := tx.ExecContext(ctx, `
INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (?, ?, ?, ?)
ON CONFLICT(account_id, person_id) DO UPDATE SET role = excluded.role`, id, persons[name], familyID, role)
			if err != nil {
				return nil, fmt.Errorf("seed: account %q member %q: %w", p.Name, name, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"familyId": familyID, "persons": persons, "accounts": accounts}, nil
}

// resetOperatorPassword is the recovery path for a forgotten operator password (there is no web route for it).
// It also logs that operator out everywhere. Run by hand, IAM-protected, like create-operator.
func resetOperatorPassword(ctx context.Context, db *sql.DB, e Event) (any, error) {
	if len(e.Password) < 12 {
		return nil, errors.New("password must be 12+ characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(e.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, "UPDATE operator SET password_hash = ? WHERE name = ? RETURNING id", string(hash), e.Name).Scan(&id); err != nil {
		return nil, fmt.Errorf("operator %q: %w", e.Name, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM operator_session WHERE operator_id = ?", id); err != nil {
		return nil, err
	}
	return map[string]any{"operatorId": id}, tx.Commit()
}

// createOperator adds a platform operator (admin_operator.yaml). Run by hand, IAM-protected:
//
//	aws lambda invoke --function-name vbank-api --cli-binary-format raw-in-base64-out --payload '{"task":"create-operator","name":"me","password":"..."}' out.json
func createOperator(ctx context.Context, db *sql.DB, e Event) (any, error) {
	if e.Name == "" || len(e.Password) < 12 {
		return nil, errors.New("name required, password must be 12+ characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(e.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	res, err := db.ExecContext(ctx, "INSERT INTO operator (name, password_hash) VALUES (?, ?)", e.Name, string(hash))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return map[string]any{"operatorId": id}, nil
}

func fundDaily(ctx context.Context, db *sql.DB, e Event) (any, error) {
	at := time.Now().UTC()
	if e.ScheduledTime != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, e.ScheduledTime); err != nil {
			return nil, err
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT id, timezone FROM family WHERE closed_at IS NULL")
	if err != nil {
		return nil, err
	}
	type family struct {
		id int64
		tz string
	}
	var fams []family
	for rows.Next() {
		var f family
		if err := rows.Scan(&f.id, &f.tz); err != nil {
			rows.Close()
			return nil, err
		}
		fams = append(fams, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var funded, skippedTotal int64
	var errs []error // one bad timezone must not stop the other families
	for _, f := range fams {
		date, due, err := dueDate(at, f.tz)
		if err != nil {
			errs = append(errs, fmt.Errorf("family %d: %w", f.id, err))
			continue
		}
		if !due && !e.Force {
			continue
		}
		n, err := data.FundDaily(ctx, db, f.id, date)
		if err != nil {
			errs = append(errs, fmt.Errorf("family %d: %w", f.id, err))
			continue
		}
		funded += n
		var skipped int64
		if err := db.QueryRowContext(ctx, `
SELECT count(*) FROM funding_skip s JOIN account a ON a.id = s.account_id
WHERE a.family_id = ? AND a.closed_at IS NULL AND a.daily_funding > 0 AND s.skip_date = ?`, f.id, date).Scan(&skipped); err != nil {
			errs = append(errs, fmt.Errorf("family %d: %w", f.id, err))
			continue
		}
		skippedTotal += skipped
	}
	return map[string]any{"funded": funded, "skipped": skippedTotal}, errors.Join(errs...)
}

// dueDate is the family's local calendar day if the scheduled time falls in its 23:00 hour. It comes from
// the schedule, not the clock, so a retry after midnight still funds the right day.
// ponytail: zones with a :30 or :45 offset fund up to 45 minutes before their local midnight.
func dueDate(at time.Time, tz string) (string, bool, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", false, err
	}
	local := at.In(loc)
	return local.Format(time.DateOnly), local.Hour() == 23, nil
}
