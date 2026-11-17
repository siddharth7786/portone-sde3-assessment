package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/lib/pq"
)

//go:embed report.sql
var reportSQL string

func main() {
	settlement := flag.String("settlement-id", "", "settlement ID to reconcile")
	released := flag.Bool("released-activity", false, "summarize released settlement activity separately from deferred transactions and payouts")
	flag.Parse()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || strings.TrimSpace(*settlement) == "" {
		log.Fatal("DATABASE_URL and -settlement-id are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, dsn, *settlement, *released); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dsn, settlement string, released bool) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	var report json.RawMessage
	var sourceCount, mappedCount, headerCount int
	err = db.QueryRowContext(ctx, reportSQL, settlement, released).Scan(&report, &sourceCount, &mappedCount, &headerCount)
	if err != nil {
		return fmt.Errorf("query settlement %s (run ingestion and mapping first): %w", settlement, err)
	}
	if sourceCount == 0 || sourceCount != mappedCount || headerCount != 1 {
		return fmt.Errorf("settlement %s needs complete mapping and one settlement header (raw rows: %d, mapped source rows: %d, headers: %d)",
			settlement, sourceCount, mappedCount, headerCount)
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
