// Lambda entry point: wires the database to the API handler and the scheduled jobs.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"vbank/api"
	"vbank/data"
	"vbank/jobs"
)

// dispatch sends scheduler events to jobs and everything else (Function URL requests) to the API.
func dispatch(db *sql.DB) func(context.Context, json.RawMessage) (any, error) {
	web := api.Handler(db, os.Getenv("ORIGIN_SECRET"))
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var e jobs.Event
		if json.Unmarshal(raw, &e) == nil && e.Task != "" {
			return jobs.Run(ctx, db, e)
		}
		var req api.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return web(ctx, req)
	}
}

func main() {
	// DB_PATH and ORIGIN_SECRET come from iac/main.tf. The migrations folder sits next to bootstrap.
	// The database is opened once and reused while the instance is warm.
	db, err := data.Open(context.Background(), os.Getenv("DB_PATH"), "migrations")
	if err != nil {
		log.Fatal(err)
	}
	lambda.Start(dispatch(db))
}
