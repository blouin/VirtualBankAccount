// Local development server: the same routes and jobs as the Lambda, on a SQLite file, without AWS.
// There is no origin secret here (that check belongs to CloudFront); do not expose this to a network.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"vbank/api"
	"vbank/data"
	"vbank/jobs"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dbPath := flag.String("db", "local.db", "SQLite file (created and migrated if missing)")
	migrations := flag.String("migrations", "../../db", "folder with the NNN_*.sql migrations")
	task := flag.String("task", "", `run one job and exit: JSON, or @file.json (same payload as the Lambda, e.g. {"task":"seed-poc",...})`)
	flag.Parse()

	db, err := data.Open(context.Background(), *dbPath, *migrations)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if *task != "" {
		raw := []byte(*task)
		if name, ok := strings.CutPrefix(*task, "@"); ok {
			if raw, err = os.ReadFile(name); err != nil {
				log.Fatal(err)
			}
		}
		var e jobs.Event
		if err := json.Unmarshal(raw, &e); err != nil {
			log.Fatal(err)
		}
		res, err := jobs.Run(context.Background(), db, e)
		if err != nil {
			log.Fatal(err)
		}
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		return
	}

	log.Printf("listening on http://%s/api/v1 (db %s)", *addr, *dbPath)
	log.Fatal(http.ListenAndServe(*addr, routes(db)))
}

// routes serves the API under /api/v1, as CloudFront does for the Lambda.
func routes(db *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", api.NewMux(db)))
	return mux
}
