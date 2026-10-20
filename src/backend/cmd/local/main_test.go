package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"vbank/data"
	"vbank/jobs"
)

func TestRoutes(t *testing.T) {
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seed := jobs.Event{Task: "seed-poc", Family: &jobs.SeedFamily{Code: "POC", Name: "POC Family"}, People: []jobs.SeedPerson{
		{Name: "Parent 1", Password: "parent1-secret", IsAdmin: true}, {Name: "Child 1", Password: "child1-secret"}}}
	if _, err := jobs.Run(ctx, db, seed); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(routes(db))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/api/v1/auth/persons?familyCode=POC")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 512)
	n, _ := res.Body.Read(buf)
	b.Write(buf[:n])
	if res.StatusCode != 200 || !strings.Contains(b.String(), `"Child 1"`) {
		t.Fatalf("persons = %d %s", res.StatusCode, b.String())
	}

	login := `{"familyCode":"POC","personId":2,"password":"child1-secret"}`
	res2, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(login))
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Fatalf("login status %d", res2.StatusCode)
	}
}
