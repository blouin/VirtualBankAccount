package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// retryAfter is 0 when the caller may try a login, else the seconds until a failure leaves the 15-minute window.
// personID 0 means no known person (only the IP limit applies).
func (s *server) retryAfter(ctx context.Context, personID int64, ip string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(MAX(MAX(1, CAST(strftime('%s', oldest, '+15 minutes') AS INTEGER) - CAST(strftime('%s', 'now') AS INTEGER))), 0)
FROM login_failures_15m
WHERE (kind = 'person' AND key = ? AND failures >= ?) OR (kind = 'ip' AND key = ? AND failures >= ?)`,
		strconv.FormatInt(personID, 10), loginPerLimit, ip, loginIPLimit).Scan(&n)
	return n, err
}

func (s *server) recordAttempt(ctx context.Context, personID int64, ip string, ok bool) error {
	var pid any
	if personID != 0 {
		pid = personID
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO login_attempt (person_id, ip, succeeded) VALUES (?, ?, ?)", pid, ip, ok)
	return err
}

func (s *server) listPersons(w http.ResponseWriter, r *http.Request) error {
	if retry, ok := s.persons.allow(r.RemoteAddr); !ok {
		return apiError{status: 429, msg: "too many requests", retry: retry}
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT p.id, p.name FROM person p JOIN family f ON f.id = p.family_id
WHERE f.code = ? AND f.closed_at IS NULL AND p.closed_at IS NULL ORDER BY p.name, p.id`, r.URL.Query().Get("familyCode"))
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Name); err != nil {
			return err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		FamilyCode string `json:"familyCode"`
		PersonID   int64  `json:"personId"`
		Password   string `json:"password"`
		Remember   bool   `json:"remember"`
		Device     string `json:"device"`
	}
	if err := readJSON(r, &in, true); err != nil {
		return err
	}
	if in.FamilyCode == "" || in.PersonID < 1 || in.Password == "" || len(in.Password) > 72 || utf8.RuneCountInString(in.Device) > maxDevice {
		return fail(400, "familyCode, personId and password (up to 72 bytes) are required; device is up to 100 characters")
	}
	ctx, ip := r.Context(), r.RemoteAddr

	var name, hash string
	var isAdmin bool
	err := s.db.QueryRowContext(ctx, `
SELECT p.name, p.password_hash, p.is_admin FROM person p JOIN family f ON f.id = p.family_id
WHERE f.code = ? AND p.id = ? AND p.closed_at IS NULL AND f.closed_at IS NULL`, in.FamilyCode, in.PersonID).Scan(&name, &hash, &isAdmin)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	pid := int64(0)
	if known {
		pid = in.PersonID
	}
	if n, err := s.retryAfter(ctx, pid, ip); err != nil {
		return err
	} else if n > 0 {
		return apiError{status: 429, msg: "too many failed attempts", retry: n}
	}
	// ponytail: an unknown person skips the bcrypt compare, so it answers faster than a wrong password.
	if !known || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		if err := s.recordAttempt(ctx, pid, ip, false); err != nil {
			return err
		}
		return fail(401, "invalid credentials")
	}
	if err := s.recordAttempt(ctx, pid, ip, true); err != nil {
		return err
	}

	tok, err := newToken()
	if err != nil {
		return err
	}
	var device any
	if in.Device != "" {
		device = in.Device
	}
	var expires sql.NullString
	err = s.db.QueryRowContext(ctx, `
INSERT INTO session (token_hash, person_id, device, expires_at)
VALUES (?, ?, ?, CASE WHEN ? THEN NULL ELSE datetime('now', '+' || ? || ' days') END) RETURNING expires_at`,
		hashToken(tok), pid, device, in.Remember, sessionDays).Scan(&expires)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{
		"token":     tok,
		"expiresAt": tsp(expires),
		"person":    map[string]any{"id": pid, "name": name, "isAdmin": isAdmin},
	})
	return nil
}

func (s *server) logoff(w http.ResponseWriter, r *http.Request, u *user) error {
	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM session WHERE id = ?", u.sessionID); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request, u *user) error {
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := readJSON(r, &in, true); err != nil {
		return err
	}
	if len(in.CurrentPassword) > 72 || len(in.NewPassword) < 6 || len(in.NewPassword) > 72 {
		return fail(400, "newPassword must be 6 to 72 bytes")
	}
	ctx, ip := r.Context(), r.RemoteAddr
	if n, err := s.retryAfter(ctx, u.personID, ip); err != nil {
		return err
	} else if n > 0 {
		return apiError{status: 429, msg: "too many failed attempts", retry: n}
	}
	var hash string
	if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM person WHERE id = ?", u.personID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.CurrentPassword)) != nil {
		if err := s.recordAttempt(ctx, u.personID, ip, false); err != nil {
			return err
		}
		return fail(403, "current password is wrong")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE person SET password_hash = ? WHERE id = ?", string(newHash), u.personID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM session WHERE person_id = ? AND id <> ?", u.personID, u.sessionID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

func (s *server) listSessions(w http.ResponseWriter, r *http.Request, u *user) error {
	rows, err := s.db.QueryContext(r.Context(), `
SELECT id, device, created_at, last_used_at, expires_at FROM session
WHERE person_id = ? AND (expires_at IS NULL OR expires_at > datetime('now')) ORDER BY created_at DESC, id DESC`, u.personID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		ID         int64   `json:"id"`
		Device     *string `json:"device"`
		CreatedAt  string  `json:"createdAt"`
		LastUsedAt *string `json:"lastUsedAt"`
		ExpiresAt  *string `json:"expiresAt"`
		Current    bool    `json:"current"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		var device, created, used, expires sql.NullString
		if err := rows.Scan(&it.ID, &device, &created, &used, &expires); err != nil {
			return err
		}
		it.Device, it.CreatedAt, it.LastUsedAt, it.ExpiresAt = strp(device), ts(created.String), tsp(used), tsp(expires)
		it.Current = it.ID == u.sessionID
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *server) revokeSession(w http.ResponseWriter, r *http.Request, u *user) error {
	id, err := pathID(r, "sessionId")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM session WHERE id = ? AND person_id = ?", id, u.personID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fail(404, "session not found")
	}
	w.WriteHeader(204)
	return nil
}
