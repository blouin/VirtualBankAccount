package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"vbank/data"
)

func TestDispatch(t *testing.T) {
	t.Setenv("ORIGIN_SECRET", "s3cret")
	ctx := context.Background()
	db, err := data.Open(ctx, filepath.Join(t.TempDir(), "bank.db"), "../../../../db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	run := dispatch(db)

	res, err := run(ctx, json.RawMessage(`{"task":"daily-funding","scheduledTime":"2026-10-20T03:59:00Z"}`))
	if m, _ := res.(map[string]any); err != nil || m["funded"] != int64(0) {
		t.Fatalf("job = %v, %v", res, err)
	}

	// a web request still reaches the API handler, even if its body mentions a task
	res, err = run(ctx, json.RawMessage(`{"rawPath":"/api/v1/auth/persons","requestContext":{"http":{"method":"GET"}},"headers":{"x-origin-secret":"s3cret"},"body":"{\"task\":\"daily-funding\"}"}`))
	if r, ok := res.(events.LambdaFunctionURLResponse); err != nil || !ok || r.StatusCode != 200 {
		t.Fatalf("web request = %v, %v", res, err)
	}
}
