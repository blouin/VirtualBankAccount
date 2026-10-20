package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // bundle the zone database; the Lambda image's may be missing
	"unicode/utf8"
)

const (
	maxBody       = 64 << 10
	maxAmount     = 100_000_000 // cents, as in the specs
	maxComment    = 200
	maxDevice     = 100
	loginPerLimit = 5  // failures per person in 15 minutes
	loginIPLimit  = 20 // failures per IP in 15 minutes
	sessionDays   = 7
	latestTxns    = 10
)

type server struct {
	db      *sql.DB
	persons *ipLimiter
}

// NewMux returns the router for the implemented routes. Paths are relative to the /api/v1 prefix.
// The spec-vs-router test in api_test.go fails when a route in auth.yaml or operations.yaml is missing here.
func NewMux(db *sql.DB) http.Handler {
	s := &server{db: db, persons: &ipLimiter{limit: 20, window: time.Minute, hits: map[string][]time.Time{}}}
	m := http.NewServeMux()
	m.HandleFunc("GET /auth/persons", wrap(s.listPersons))
	m.HandleFunc("POST /auth/login", wrap(s.login))
	m.HandleFunc("POST /auth/logoff", s.authed(s.logoff))
	m.HandleFunc("PUT /auth/changePassword", s.authed(s.changePassword))
	m.HandleFunc("GET /auth/sessions", s.authed(s.listSessions))
	m.HandleFunc("DELETE /auth/sessions/{sessionId}", s.authed(s.revokeSession))

	m.HandleFunc("GET /accounts", s.authed(s.listAccounts))
	m.HandleFunc("GET /accounts/{accountId}", s.authed(s.getAccount))
	m.HandleFunc("GET /accounts/{accountId}/funding-skips", s.authed(s.listFundingSkips))
	m.HandleFunc("PUT /accounts/{accountId}/funding-skips/{date}", s.authed(s.skipFunding))
	m.HandleFunc("DELETE /accounts/{accountId}/funding-skips/{date}", s.authed(s.unskipFunding))
	m.HandleFunc("GET /accounts/{accountId}/transactions", s.authed(s.listTransactions))
	m.HandleFunc("POST /accounts/{accountId}/transactions", s.authed(s.createTransaction))
	m.HandleFunc("POST /requests", s.authed(s.createRequest))
	m.HandleFunc("GET /requests", s.authed(s.listRequests))
	m.HandleFunc("POST /requests/{requestId}/approve", s.authed(s.decide("approved")))
	m.HandleFunc("POST /requests/{requestId}/reject", s.authed(s.decide("rejected")))
	m.HandleFunc("/", wrap(func(http.ResponseWriter, *http.Request) error { return fail(404, "not found") }))
	return m
}

// apiError is an error with a status; anything else becomes a logged 500.
type apiError struct {
	status int
	msg    string
	retry  int // Retry-After seconds for 429
}

func (e apiError) Error() string { return e.msg }

func fail(status int, msg string) error { return apiError{status: status, msg: msg} }

func wrap(h func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		err := h(w, r)
		if err == nil {
			return
		}
		var ae apiError
		if !errors.As(err, &ae) {
			log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
			ae = apiError{status: 500, msg: "internal error"}
		}
		if ae.retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(ae.retry))
		}
		writeJSON(w, ae.status, map[string]string{"message": ae.msg})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonResponse(status int, v any) Response {
	body, _ := json.Marshal(v)
	return Response{StatusCode: status, Headers: map[string]string{"content-type": "application/json"}, Body: string(body)}
}

// readJSON decodes the body into v; required says an empty body is a 400 rather than "no fields".
func readJSON(r *http.Request, v any, required bool) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return fail(400, "request body too large or unreadable")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		if required {
			return fail(400, "request body required")
		}
		return nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fail(400, "invalid JSON body")
	}
	return nil
}

func pathIDValue(v string) (int64, error) {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 1 {
		return 0, fail(404, "not found")
	}
	return id, nil
}

func pathID(r *http.Request, name string) (int64, error) { return pathIDValue(r.PathValue(name)) }

// ts converts SQLite's "YYYY-MM-DD HH:MM:SS" (UTC) to RFC 3339, as the specs say.
func ts(s string) string {
	t, err := time.Parse(time.DateTime, s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

func tsp(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := ts(s.String)
	return &v
}

func strp(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func intp(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

// optComment turns the optional comment into a column value (nil = NULL) or a 400.
func optComment(c *string) (any, error) {
	if c == nil || *c == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(*c) > maxComment {
		return nil, fail(400, "comment is longer than 200 characters")
	}
	return *c, nil
}

func paging(r *http.Request) (page, size int, err error) {
	page, size = 1, 20
	q := r.URL.Query()
	if v := q.Get("page"); v != "" {
		if page, err = strconv.Atoi(v); err != nil || page < 1 {
			return 0, 0, fail(400, "page must be 1 or more")
		}
	}
	if v := q.Get("pageSize"); v != "" {
		if size, err = strconv.Atoi(v); err != nil || size < 1 || size > 100 {
			return 0, 0, fail(400, "pageSize must be 1 to 100")
		}
	}
	return page, size, nil
}

func idempotencyKey(r *http.Request) (string, error) {
	k := r.Header.Get("Idempotency-Key")
	if len(k) > 64 {
		return "", fail(400, "Idempotency-Key is longer than 64 characters")
	}
	return k, nil
}

// dbError maps the messages the schema triggers raise to the statuses in the specs.
func dbError(err error) error {
	m := err.Error()
	switch {
	case strings.Contains(m, "insufficient funds"):
		return fail(409, "insufficient funds")
	case strings.Contains(m, "account or family is closed"):
		return fail(409, "account is closed")
	case strings.Contains(m, "request already decided"):
		return fail(409, "request already decided")
	case strings.Contains(m, "not authorized on account"):
		return fail(403, "not authorized on this account")
	case strings.Contains(m, "idempotency"):
		return fail(409, "Idempotency-Key was already used")
	}
	return err
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// user is the caller, loaded from the session token.
type user struct {
	sessionID int64
	personID  int64
	familyID  int64
	name      string
	isAdmin   bool
	tz        string
}

// today is the family's local calendar day (the day funding skips refer to).
func (u *user) today() string {
	loc, err := time.LoadLocation(u.tz)
	if err != nil {
		loc = time.UTC
	}
	return time.Now().In(loc).Format(time.DateOnly)
}

// authed requires a valid session: not expired, person and family open. It also stamps last_used_at at most hourly.
func (s *server) authed(h func(http.ResponseWriter, *http.Request, *user) error) http.HandlerFunc {
	return wrap(func(w http.ResponseWriter, r *http.Request) error {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			return fail(401, "login required")
		}
		u := &user{}
		var stale bool
		err := s.db.QueryRowContext(r.Context(), `
SELECT s.id, p.id, p.family_id, p.name, p.is_admin, f.timezone,
       s.last_used_at IS NULL OR s.last_used_at < datetime('now', '-1 hour')
FROM session s JOIN person p ON p.id = s.person_id JOIN family f ON f.id = p.family_id
WHERE s.token_hash = ? AND (s.expires_at IS NULL OR s.expires_at > datetime('now'))
  AND p.closed_at IS NULL AND f.closed_at IS NULL`, hashToken(tok)).
			Scan(&u.sessionID, &u.personID, &u.familyID, &u.name, &u.isAdmin, &u.tz, &stale)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(401, "login required")
		}
		if err != nil {
			return err
		}
		if stale {
			if _, err := s.db.ExecContext(r.Context(), "UPDATE session SET last_used_at = datetime('now') WHERE id = ?", u.sessionID); err != nil {
				return err
			}
		}
		return h(w, r, u)
	})
}

// ipLimiter is a plain in-memory request counter for GET /auth/persons. Lambda runs one instance
// (reserved concurrency 1), so memory is enough; move it to a table if that changes.
type ipLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

// allow records a hit and returns the seconds to wait when the IP is over the limit.
func (l *ipLimiter) allow(ip string) (retry int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 10000 {
		l.hits = map[string][]time.Time{}
	}
	keep := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.limit {
		l.hits[ip] = keep
		return max(1, int((l.window-now.Sub(keep[0])).Seconds())+1), false
	}
	l.hits[ip] = append(keep, now)
	return 0, true
}
