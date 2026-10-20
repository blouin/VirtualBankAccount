package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

type txnJSON struct {
	ID          int64   `json:"id"`
	AccountID   int64   `json:"accountId"`
	PersonID    *int64  `json:"personId"`
	Type        string  `json:"type"`
	Amount      int64   `json:"amount"`
	RequestID   *int64  `json:"requestId"`
	FundingDate *string `json:"fundingDate"`
	Comment     *string `json:"comment"`
	CreatedAt   string  `json:"createdAt"`
}

type requestJSON struct {
	ID               int64   `json:"id"`
	AccountID        int64   `json:"accountId"`
	Amount           int64   `json:"amount"`
	Status           string  `json:"status"`
	RequestedBy      int64   `json:"requestedBy"`
	RequesterComment *string `json:"requesterComment"`
	DecidedBy        *int64  `json:"decidedBy"`
	DeciderComment   *string `json:"deciderComment"`
	RequestedAt      string  `json:"requestedAt"`
	DecidedAt        *string `json:"decidedAt"`
}

type balanceJSON struct {
	AccountID    int64   `json:"accountId"`
	Name         string  `json:"name"`
	ClosedAt     *string `json:"closedAt"`
	Role         string  `json:"role"`
	Balance      int64   `json:"balance"`
	Pending      int64   `json:"pending"`
	Total        int64   `json:"total"`
	DailyFunding int64   `json:"dailyFunding"`
	SkippedToday bool    `json:"skippedToday"`
}

type skipJSON struct {
	Date      string  `json:"date"`
	CreatedBy int64   `json:"createdBy"`
	Comment   *string `json:"comment"`
	CreatedAt string  `json:"createdAt"`
}

type scanner interface{ Scan(...any) error }

const txnCols = "id, account_id, person_id, type, amount, request_id, funding_date, comment, created_at"

func scanTxn(r scanner) (t txnJSON, err error) {
	var pid, rid sql.NullInt64
	var fd, cm sql.NullString
	if err = r.Scan(&t.ID, &t.AccountID, &pid, &t.Type, &t.Amount, &rid, &fd, &cm, &t.CreatedAt); err != nil {
		return
	}
	t.PersonID, t.RequestID, t.FundingDate, t.Comment, t.CreatedAt = intp(pid), intp(rid), strp(fd), strp(cm), ts(t.CreatedAt)
	return
}

const reqCols = "id, account_id, amount, status, requested_by, requester_comment, decided_by, decider_comment, requested_at, decided_at"

func scanReq(r scanner) (q requestJSON, err error) {
	var rc, dc, da sql.NullString
	var db sql.NullInt64
	if err = r.Scan(&q.ID, &q.AccountID, &q.Amount, &q.Status, &q.RequestedBy, &rc, &db, &dc, &q.RequestedAt, &da); err != nil {
		return
	}
	q.RequesterComment, q.DecidedBy, q.DeciderComment, q.RequestedAt, q.DecidedAt = strp(rc), intp(db), strp(dc), ts(q.RequestedAt), tsp(da)
	return
}

// member returns the caller's role on the account and whether it is closed; 404 when not a member
// (also for other families' accounts, so ids can't be probed).
func (s *server) member(ctx context.Context, u *user, accountID int64) (role string, closed bool, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT ar.role, a.closed_at IS NOT NULL FROM account_role ar JOIN account a ON a.id = ar.account_id AND a.family_id = ar.family_id
WHERE ar.account_id = ? AND ar.person_id = ? AND ar.family_id = ?`, accountID, u.personID, u.familyID).Scan(&role, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		err = fail(404, "account not found")
	}
	return
}

const balanceSQL = `
SELECT b.account_id, b.name, b.closed_at, ar.role, b.balance, b.pending, b.total, a.daily_funding,
       EXISTS (SELECT 1 FROM funding_skip fs WHERE fs.account_id = b.account_id AND fs.skip_date = ?)
FROM account_balance b JOIN account a ON a.id = b.account_id
JOIN account_role ar ON ar.account_id = b.account_id AND ar.person_id = ?`

func scanBalance(r scanner) (b balanceJSON, err error) {
	var closed sql.NullString
	if err = r.Scan(&b.AccountID, &b.Name, &closed, &b.Role, &b.Balance, &b.Pending, &b.Total, &b.DailyFunding, &b.SkippedToday); err != nil {
		return
	}
	b.ClosedAt = tsp(closed)
	return
}

func (s *server) listAccounts(w http.ResponseWriter, r *http.Request, u *user) error {
	q := balanceSQL
	if r.URL.Query().Get("includeClosed") != "true" {
		q += " WHERE b.closed_at IS NULL"
	}
	rows, err := s.db.QueryContext(r.Context(), q+" ORDER BY b.name, b.account_id", u.today(), u.personID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []balanceJSON{}
	for rows.Next() {
		b, err := scanBalance(rows)
		if err != nil {
			return err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *server) getAccount(w http.ResponseWriter, r *http.Request, u *user) error {
	id, err := pathID(r, "accountId")
	if err != nil {
		return err
	}
	if _, _, err := s.member(r.Context(), u, id); err != nil {
		return err
	}
	b, err := scanBalance(s.db.QueryRowContext(r.Context(), balanceSQL+" WHERE b.account_id = ?", u.today(), u.personID, id))
	if err != nil {
		return err
	}
	txns, _, err := s.queryTxns(r.Context(), id, 1, latestTxns)
	if err != nil {
		return err
	}
	writeJSON(w, 200, struct {
		balanceJSON
		Transactions []txnJSON `json:"transactions"`
	}{b, txns})
	return nil
}

func (s *server) queryTxns(ctx context.Context, accountID int64, page, size int) ([]txnJSON, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_transaction WHERE account_id = ?", accountID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+txnCols+" FROM account_transaction WHERE account_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?",
		accountID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []txnJSON{}
	for rows.Next() {
		t, err := scanTxn(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func (s *server) listTransactions(w http.ResponseWriter, r *http.Request, u *user) error {
	id, err := pathID(r, "accountId")
	if err != nil {
		return err
	}
	page, size, err := paging(r)
	if err != nil {
		return err
	}
	if _, _, err := s.member(r.Context(), u, id); err != nil {
		return err
	}
	items, total, err := s.queryTxns(r.Context(), id, page, size)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "pageSize": size, "total": total})
	return nil
}

func (s *server) createTransaction(w http.ResponseWriter, r *http.Request, u *user) error {
	id, err := pathID(r, "accountId")
	if err != nil {
		return err
	}
	var in struct {
		Type    string  `json:"type"`
		Amount  int64   `json:"amount"`
		Comment *string `json:"comment"`
	}
	if err := readJSON(r, &in, true); err != nil {
		return err
	}
	if (in.Type != "deposit" && in.Type != "withdrawal") || in.Amount < 1 || in.Amount > maxAmount {
		return fail(400, "type must be deposit or withdrawal; amount 1 to 100000000 cents")
	}
	comment, err := optComment(in.Comment)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	role, closed, err := s.member(ctx, u, id)
	if err != nil {
		return err
	}
	if key != "" {
		t, err := scanTxn(s.db.QueryRowContext(ctx, "SELECT "+txnCols+" FROM account_transaction WHERE person_id = ? AND idempotency_key = ?", u.personID, key))
		if err == nil {
			if t.AccountID != id || t.Type != in.Type || t.Amount != in.Amount {
				return fail(409, "Idempotency-Key was used with different content")
			}
			writeJSON(w, 200, t)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if in.Type == "deposit" && role != "manager" {
		return fail(403, "only a manager can deposit; request a deposit instead")
	}
	if closed {
		return fail(409, "account is closed")
	}
	var k any
	if key != "" {
		k = key
	}
	t, err := scanTxn(s.db.QueryRowContext(ctx, "INSERT INTO account_transaction (account_id, person_id, type, amount, idempotency_key, comment) VALUES (?, ?, ?, ?, ?, ?) RETURNING "+txnCols,
		id, u.personID, in.Type, in.Amount, k, comment))
	if err != nil {
		return dbError(err)
	}
	writeJSON(w, 201, t)
	return nil
}

func (s *server) createRequest(w http.ResponseWriter, r *http.Request, u *user) error {
	var in struct {
		AccountID int64   `json:"accountId"`
		Amount    int64   `json:"amount"`
		Comment   *string `json:"comment"`
	}
	if err := readJSON(r, &in, true); err != nil {
		return err
	}
	if in.AccountID < 1 || in.Amount < 1 || in.Amount > maxAmount {
		return fail(400, "accountId and amount (1 to 100000000 cents) are required")
	}
	comment, err := optComment(in.Comment)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	_, closed, err := s.member(ctx, u, in.AccountID)
	if err != nil {
		return err
	}
	if key != "" {
		q, err := scanReq(s.db.QueryRowContext(ctx, "SELECT "+reqCols+" FROM transaction_request WHERE requested_by = ? AND idempotency_key = ?", u.personID, key))
		if err == nil {
			if q.AccountID != in.AccountID || q.Amount != in.Amount {
				return fail(409, "Idempotency-Key was used with different content")
			}
			writeJSON(w, 200, q)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if closed {
		return fail(409, "account is closed")
	}
	var k any
	if key != "" {
		k = key
	}
	q, err := scanReq(s.db.QueryRowContext(ctx, "INSERT INTO transaction_request (account_id, amount, requested_by, requester_comment, idempotency_key) VALUES (?, ?, ?, ?, ?) RETURNING "+reqCols,
		in.AccountID, in.Amount, u.personID, comment, k))
	if err != nil {
		return dbError(err)
	}
	writeJSON(w, 201, q)
	return nil
}

func (s *server) listRequests(w http.ResponseWriter, r *http.Request, u *user) error {
	page, size, err := paging(r)
	if err != nil {
		return err
	}
	where, args := " WHERE ar.person_id = ?", []any{u.personID}
	qv := r.URL.Query()
	if v := qv.Get("accountId"); v != "" {
		id, err := pathIDValue(v)
		if err != nil {
			return fail(400, "accountId must be a number")
		}
		if _, _, err := s.member(r.Context(), u, id); err != nil {
			return err
		}
		where += " AND r.account_id = ?"
		args = append(args, id)
	}
	if v := qv.Get("status"); v != "" {
		if v != "pending" && v != "approved" && v != "rejected" {
			return fail(400, "status must be pending, approved or rejected")
		}
		where += " AND r.status = ?"
		args = append(args, v)
	}
	from := " FROM transaction_request r JOIN account_role ar ON ar.account_id = r.account_id" + where
	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*)"+from, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT r.id, r.account_id, r.amount, r.status, r.requested_by, r.requester_comment, r.decided_by, r.decider_comment, r.requested_at, r.decided_at"+
		from+" ORDER BY r.requested_at DESC, r.id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []requestJSON{}
	for rows.Next() {
		q, err := scanReq(rows)
		if err != nil {
			return err
		}
		items = append(items, q)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "pageSize": size, "total": total})
	return nil
}

func (s *server) decide(status string) func(http.ResponseWriter, *http.Request, *user) error {
	return func(w http.ResponseWriter, r *http.Request, u *user) error {
		id, err := pathID(r, "requestId")
		if err != nil {
			return err
		}
		var in struct {
			Comment *string `json:"comment"`
		}
		if err := readJSON(r, &in, false); err != nil {
			return err
		}
		comment, err := optComment(in.Comment)
		if err != nil {
			return err
		}
		ctx := r.Context()
		var cur, role string
		var closed bool
		err = s.db.QueryRowContext(ctx, `
SELECT r.status, ar.role, a.closed_at IS NOT NULL FROM transaction_request r
JOIN account a ON a.id = r.account_id JOIN account_role ar ON ar.account_id = r.account_id AND ar.person_id = ?
WHERE r.id = ?`, u.personID, id).Scan(&cur, &role, &closed)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "request not found")
		}
		if err != nil {
			return err
		}
		switch {
		case role != "manager":
			return fail(403, "only a manager can decide a request")
		case cur != "pending":
			return fail(409, "request already decided")
		case closed && status == "approved":
			return fail(409, "account is closed")
		}
		q, err := scanReq(s.db.QueryRowContext(ctx, `
UPDATE transaction_request SET status = ?, decided_by = ?, decided_at = datetime('now'), decider_comment = ?
WHERE id = ? AND status = 'pending' RETURNING `+reqCols, status, u.personID, comment, id))
		if err != nil {
			return dbError(err)
		}
		writeJSON(w, 200, q)
		return nil
	}
}

// skipDate validates the {date} path value: a real calendar day, today or later in the family's time zone.
func skipDate(r *http.Request, u *user) (string, error) {
	d := r.PathValue("date")
	t, err := time.Parse(time.DateOnly, d)
	if err != nil || t.Format(time.DateOnly) != d {
		return "", fail(400, "date must be YYYY-MM-DD")
	}
	if d < u.today() {
		return "", fail(400, "date is in the past")
	}
	return d, nil
}

func (s *server) listFundingSkips(w http.ResponseWriter, r *http.Request, u *user) error {
	id, err := pathID(r, "accountId")
	if err != nil {
		return err
	}
	if _, _, err := s.member(r.Context(), u, id); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT skip_date, created_by, comment, created_at FROM funding_skip WHERE account_id = ? AND skip_date >= ? ORDER BY skip_date",
		id, u.today())
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []skipJSON{}
	for rows.Next() {
		var k skipJSON
		var c sql.NullString
		if err := rows.Scan(&k.Date, &k.CreatedBy, &c, &k.CreatedAt); err != nil {
			return err
		}
		k.Comment, k.CreatedAt = strp(c), ts(k.CreatedAt)
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *server) skipFunding(w http.ResponseWriter, r *http.Request, u *user) error {
	id, err := pathID(r, "accountId")
	if err != nil {
		return err
	}
	var in struct {
		Comment *string `json:"comment"`
	}
	if err := readJSON(r, &in, false); err != nil {
		return err
	}
	comment, err := optComment(in.Comment)
	if err != nil {
		return err
	}
	ctx := r.Context()
	role, closed, err := s.member(ctx, u, id)
	if err != nil {
		return err
	}
	if role != "manager" {
		return fail(403, "only a manager can skip funding")
	}
	d, err := skipDate(r, u)
	if err != nil {
		return err
	}
	if closed {
		return fail(409, "account is closed")
	}
	var k skipJSON
	var c sql.NullString
	err = s.db.QueryRowContext(ctx, `
INSERT INTO funding_skip (account_id, skip_date, created_by, comment) VALUES (?, ?, ?, ?)
ON CONFLICT (account_id, skip_date) DO UPDATE SET comment = excluded.comment
RETURNING skip_date, created_by, comment, created_at`, id, d, u.personID, comment).Scan(&k.Date, &k.CreatedBy, &c, &k.CreatedAt)
	if err != nil {
		return dbError(err)
	}
	k.Comment, k.CreatedAt = strp(c), ts(k.CreatedAt)
	writeJSON(w, 200, k)
	return nil
}

func (s *server) unskipFunding(w http.ResponseWriter, r *http.Request, u *user) error {
	id, err := pathID(r, "accountId")
	if err != nil {
		return err
	}
	role, _, err := s.member(r.Context(), u, id)
	if err != nil {
		return err
	}
	if role != "manager" {
		return fail(403, "only a manager can remove a skip")
	}
	d, err := skipDate(r, u)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM funding_skip WHERE account_id = ? AND skip_date = ?", id, d); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}
