package data

import (
	"context"
	"database/sql"
)

// FundDaily posts the automatic daily deposit for date (YYYY-MM-DD, the family's local day being closed) to
// every open account of an open family with daily_funding > 0 and no funding_skip row for that day. A closed
// family is skipped here (not left to the trigger, which would abort the whole statement). Re-running
// is a no-op because of ux_funding_once_per_day. Returns how many accounts were funded.
func FundDaily(ctx context.Context, db *sql.DB, familyID int64, date string) (int64, error) {
	res, err := db.ExecContext(ctx, `
INSERT INTO account_transaction (account_id, person_id, type, amount, funding_date)
SELECT a.id, NULL, 'deposit', a.daily_funding, ?1
FROM account a
WHERE a.family_id = ?2 AND a.closed_at IS NULL AND a.daily_funding > 0
  AND EXISTS (SELECT 1 FROM family f WHERE f.id = a.family_id AND f.closed_at IS NULL)
  AND NOT EXISTS (SELECT 1 FROM funding_skip s WHERE s.account_id = a.id AND s.skip_date = ?1)
ON CONFLICT DO NOTHING`, date, familyID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
