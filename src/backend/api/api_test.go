package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"vbank/data"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := data.Open(context.Background(), filepath.Join(t.TempDir(), "bank.db"), "../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw1234"), bcrypt.MinCost)
	// family 1: mom (manager, id 1), kid (user, id 2); account 1 shared. family 2: other (id 3), account 2.
	for _, q := range []string{
		"INSERT INTO family (name, code) VALUES ('A', 'fam-a'), ('B', 'fam-b')",
		"INSERT INTO person (family_id, name, password_hash, is_admin) VALUES (1, 'mom', '" + string(hash) + "', 1), (1, 'kid', '" + string(hash) + "', 0), (2, 'other', '" + string(hash) + "', 1)",
		"INSERT INTO account (family_id, name) VALUES (1, 'Savings'), (2, 'Theirs')",
		"INSERT INTO account_role (account_id, person_id, family_id, role) VALUES (1, 1, 1, 'manager'), (1, 2, 1, 'user'), (2, 3, 2, 'manager')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	return db
}

type client struct {
	t   *testing.T
	h   func(context.Context, Request) (Response, error)
	tok string
	ip  string
}

func (c *client) do(method, path, body string, hdr ...string) (int, map[string]any, string) {
	c.t.Helper()
	req := Request{RawPath: pathPrefix + path, Body: body, Headers: map[string]string{"x-origin-secret": "s3cret", "x-forwarded-for": c.ip}}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		req.RawPath, req.RawQueryString = pathPrefix+path[:i], path[i+1:]
	}
	req.RequestContext.HTTP.Method = method
	if c.tok != "" {
		req.Headers["authorization"] = "Bearer " + c.tok
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Headers[hdr[i]] = hdr[i+1]
	}
	res, err := c.h(context.Background(), req)
	if err != nil {
		c.t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(res.Body), &m)
	return res.StatusCode, m, res.Body
}

func (c *client) login(person int, remember bool) map[string]any {
	c.t.Helper()
	st, m, b := c.do("POST", "/auth/login", fmt.Sprintf(`{"familyCode":"fam-a","personId":%d,"password":"pw1234","remember":%v}`, person, remember))
	if st != 200 {
		c.t.Fatal(st, b)
	}
	c.tok = m["token"].(string)
	return m
}

func newClient(t *testing.T) *client {
	return &client{t: t, h: Handler(testDB(t), "s3cret"), ip: "9.9.9.9"}
}

func expect(t *testing.T, st, want int, body string) {
	t.Helper()
	if st != want {
		t.Fatalf("status %d, want %d: %s", st, want, body)
	}
}

func TestHandler(t *testing.T) {
	c := newClient(t)
	st, _, b := c.do("GET", "/auth/persons?familyCode=fam-a", "")
	expect(t, st, 200, b)
	if !strings.Contains(b, `"mom"`) || strings.Contains(b, "other") {
		t.Fatal(b)
	}
	if _, _, b := c.do("GET", "/auth/persons?familyCode=nope", ""); b != "[]\n" {
		t.Fatal(b)
	}
	st, _, b = c.do("GET", "/nothing", "")
	expect(t, st, 404, b)
	for _, h := range []map[string]string{nil, {"x-origin-secret": "wrong"}} {
		if res, _ := c.h(context.Background(), Request{Headers: h}); res.StatusCode != 403 {
			t.Fatalf("headers %v: status %d", h, res.StatusCode)
		}
	}
}

func TestAuthFlow(t *testing.T) {
	c := newClient(t)
	st, _, b := c.do("GET", "/accounts", "")
	expect(t, st, 401, b)

	m := c.login(2, false)
	if m["expiresAt"] == nil {
		t.Fatal("non-remembered session must expire")
	}
	if m := c.login(2, true); m["expiresAt"] != nil {
		t.Fatal("remembered session must not expire")
	}
	st, _, b = c.do("GET", "/auth/sessions", "")
	expect(t, st, 200, b)
	if strings.Count(b, `"current":true`) != 1 || strings.Count(b, `"id"`) != 2 {
		t.Fatal(b)
	}

	// revoke the other session (the first one, id 1); the current one stays
	st, _, b = c.do("DELETE", "/auth/sessions/1", "")
	expect(t, st, 204, b)
	st, _, b = c.do("DELETE", "/auth/sessions/1", "")
	expect(t, st, 404, b)

	// changing the password drops other sessions and rejects a wrong current password
	other := &client{t: t, h: c.h, ip: c.ip}
	other.login(2, true)
	st, _, b = c.do("PUT", "/auth/changePassword", `{"currentPassword":"bad","newPassword":"newpw123"}`)
	expect(t, st, 403, b)
	st, _, b = c.do("PUT", "/auth/changePassword", `{"currentPassword":"pw1234","newPassword":"newpw123"}`)
	expect(t, st, 204, b)
	st, _, b = other.do("GET", "/accounts", "")
	expect(t, st, 401, b)
	st, _, b = c.do("GET", "/accounts", "")
	expect(t, st, 200, b)

	st, _, b = c.do("POST", "/auth/logoff", "")
	expect(t, st, 204, b)
	st, _, b = c.do("GET", "/accounts", "")
	expect(t, st, 401, b)
}

func TestLoginRateLimit(t *testing.T) {
	c := newClient(t)
	for i := 0; i < loginPerLimit; i++ {
		st, _, b := c.do("POST", "/auth/login", `{"familyCode":"fam-a","personId":1,"password":"wrong1"}`)
		expect(t, st, 401, b)
	}
	st, _, b := c.do("POST", "/auth/login", `{"familyCode":"fam-a","personId":1,"password":"pw1234"}`)
	expect(t, st, 429, b)
	req := Request{RawPath: pathPrefix + "/auth/login", Body: `{"familyCode":"fam-a","personId":1,"password":"pw1234"}`,
		Headers: map[string]string{"x-origin-secret": "s3cret", "x-forwarded-for": "9.9.9.9"}}
	req.RequestContext.HTTP.Method = "POST"
	res, _ := c.h(context.Background(), req)
	if res.Headers["retry-after"] == "" {
		t.Fatal("missing Retry-After")
	}
	// another person from another IP is unaffected
	c.ip = "8.8.8.8"
	st, _, b = c.do("POST", "/auth/login", `{"familyCode":"fam-a","personId":2,"password":"pw1234"}`)
	expect(t, st, 200, b)
}

func TestMoneyFlow(t *testing.T) {
	mom, kid := newClient(t), newClient(t)
	kid.h = mom.h
	mom.login(1, false)
	kid.login(2, false)

	st, _, b := kid.do("POST", "/accounts/1/transactions", `{"type":"deposit","amount":500}`)
	expect(t, st, 403, b)
	st, _, b = kid.do("POST", "/accounts/1/transactions", `{"type":"withdrawal","amount":1}`)
	expect(t, st, 409, b) // insufficient funds
	st, _, b = mom.do("POST", "/accounts/1/transactions", `{"type":"deposit","amount":1000}`, "idempotency-key", "k1")
	expect(t, st, 201, b)
	st, _, b = mom.do("POST", "/accounts/1/transactions", `{"type":"deposit","amount":1000}`, "idempotency-key", "k1")
	expect(t, st, 200, b) // replay
	st, _, b = mom.do("POST", "/accounts/1/transactions", `{"type":"deposit","amount":999}`, "idempotency-key", "k1")
	expect(t, st, 409, b)
	st, _, b = mom.do("POST", "/accounts/1/transactions", `{"type":"deposit","amount":0}`)
	expect(t, st, 400, b)
	st, _, b = mom.do("POST", "/accounts/1/transactions", `{"type":"withdrawal","amount":1,"comment":"`+strings.Repeat("x", 201)+`"}`)
	expect(t, st, 400, b)
	st, _, b = mom.do("POST", "/accounts/1/transactions", `{"type":"deposit","amount":1,"comment":"gift"}`)
	expect(t, st, 201, b)
	if !strings.Contains(b, `"comment":"gift"`) {
		t.Fatal(b)
	}
	st, _, b = mom.do("POST", "/accounts/1/transactions", `{"type":"withdrawal","amount":1}`)
	expect(t, st, 201, b) // put the cent back so the balances below stay as before
	st, _, b = kid.do("POST", "/requests", `{"accountId":1,"amount":300,"comment":"toy"}`)
	expect(t, st, 201, b)
	_, acct, _ := kid.do("GET", "/accounts/1", "")
	if acct["balance"] != 1000.0 || acct["pending"] != 300.0 || acct["total"] != 1300.0 || acct["role"] != "user" {
		t.Fatal(acct)
	}
	st, _, b = kid.do("POST", "/requests/1/approve", "")
	expect(t, st, 403, b)
	st, _, b = mom.do("POST", "/requests/1/approve", `{"comment":"ok"}`)
	expect(t, st, 200, b)
	st, _, b = mom.do("POST", "/requests/1/reject", "")
	expect(t, st, 409, b)
	_, acct, _ = kid.do("GET", "/accounts/1", "")
	if acct["balance"] != 1300.0 || acct["pending"] != 0.0 || len(acct["transactions"].([]any)) != 4 {
		t.Fatal(acct)
	}
	if first := acct["transactions"].([]any)[0].(map[string]any); first["comment"] != "toy" || first["requestId"] == nil {
		t.Fatalf("approved deposit should carry the requester's comment: %v", first)
	}
	st, _, b = kid.do("POST", "/accounts/1/transactions", `{"type":"withdrawal","amount":1300}`)
	expect(t, st, 201, b)
	st, _, b = kid.do("GET", "/requests?status=approved&pageSize=5", "")
	expect(t, st, 200, b)
	if !strings.Contains(b, `"total":1`) {
		t.Fatal(b)
	}
	st, _, b = kid.do("GET", "/accounts/1/transactions?pageSize=101", "")
	expect(t, st, 400, b)

	// another family's account looks nonexistent
	st, _, b = kid.do("GET", "/accounts/2", "")
	expect(t, st, 404, b)
	st, _, b = kid.do("POST", "/requests", `{"accountId":2,"amount":5}`)
	expect(t, st, 404, b)

	// funding skips: manager only
	day := time.Now().UTC().AddDate(0, 0, 2).Format(time.DateOnly)
	st, _, b = kid.do("PUT", "/accounts/1/funding-skips/"+day, "")
	expect(t, st, 403, b)
	st, _, b = mom.do("PUT", "/accounts/1/funding-skips/"+day, `{"comment":"trip"}`)
	expect(t, st, 200, b)
	st, _, b = mom.do("PUT", "/accounts/1/funding-skips/2000-01-01", "")
	expect(t, st, 400, b)
	_, _, b = kid.do("GET", "/accounts/1/funding-skips", "")
	if !strings.Contains(b, day) {
		t.Fatal(b)
	}
	st, _, b = mom.do("DELETE", "/accounts/1/funding-skips/"+day, "")
	expect(t, st, 204, b)
}

// routes in the implemented specs must be routed; the other specs only log their gap.
func TestSpecRoutesImplemented(t *testing.T) {
	mux := NewMux(testDB(t)).(*http.ServeMux)
	pathRe, methodRe, paramRe := regexp.MustCompile(`^  (/\S*):\s*$`), regexp.MustCompile(`^    (get|post|put|patch|delete):`), regexp.MustCompile(`\{[^}]+\}`)
	for _, spec := range []string{"auth", "operations", "admin_family", "admin_operator"} {
		raw, err := os.ReadFile("../../../spec/" + spec + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		var path string
		total, missing := 0, 0
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimRight(line, "\r")
			if m := pathRe.FindStringSubmatch(line); m != nil {
				path = m[1]
			} else if m := methodRe.FindStringSubmatch(line); m != nil && path != "" {
				total++
				r := httptest.NewRequest(strings.ToUpper(m[1]), paramRe.ReplaceAllString(path, "1"), nil)
				_, pattern := mux.Handler(r)
				if pattern == "/" {
					missing++
					if spec == "auth" || spec == "operations" {
						t.Errorf("%s: %s %s is not routed", spec, m[1], path)
					}
				}
			}
		}
		t.Logf("%s: %d of %d routes implemented", spec, total-missing, total)
	}
}

func TestClientIP(t *testing.T) {
	req := func(xff string) Request {
		r := Request{Headers: map[string]string{"x-forwarded-for": xff}}
		r.RequestContext.HTTP.SourceIP = "10.0.0.1"
		return r
	}
	for xff, want := range map[string]string{
		"9.9.9.9":                    "9.9.9.9",
		"6.6.6.6, 9.9.9.9":           "9.9.9.9", // spoofed entry on the left is ignored
		"6.6.6.6, 9.9.9.9, 10.0.0.1": "9.9.9.9", // Lambda appended the edge
		"":                           "10.0.0.1",
	} {
		if got := ClientIP(req(xff)); got != want {
			t.Errorf("xff %q = %s, want %s", xff, got, want)
		}
	}
}
