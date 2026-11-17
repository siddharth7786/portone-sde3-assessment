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
	"sort"
	"strings"
	"syscall"

	"github.com/lib/pq"
)

//go:embed schema.sql
var schema string

const paymentRulesSQL = `
SELECT source_file, source_line, transaction_type, amount_field, description, record_ref,
       to_summary_field_when_positive_amount, to_summary_field_when_negative_amount
FROM payment_mapping_rules
ORDER BY source_file, source_line`

const settlementRulesSQL = `
SELECT source_file, source_line, transaction_type, amount_type, amount_description, record_ref,
       to_summary_field_when_positive_amount, to_summary_field_when_negative_amount
FROM settlement_mapping_rules
ORDER BY source_file, source_line`

func main() {
	settlement := flag.String("settlement-id", "", "settlement ID to map")
	flag.Parse()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || strings.TrimSpace(*settlement) == "" {
		log.Fatal("DATABASE_URL and -settlement-id are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	counts, err := run(ctx, dsn, *settlement)
	if err != nil {
		log.Fatal(err)
	}
	var statuses []string
	for status := range counts {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		log.Printf("settlement %s: %s=%d", *settlement, status, counts[status])
	}
}

func run(ctx context.Context, dsn, settlement string) (map[string]int, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Use the same transaction lock as ingestion so the input rows stay stable.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(146379035, 1)"); err != nil {
		return nil, fmt.Errorf("lock ingestion and mapping: %w", err)
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("create mapping table (run ingestion first): %w", err)
	}

	payments, err := loadRules(ctx, tx, paymentRulesSQL)
	if err != nil {
		return nil, fmt.Errorf("read payment rules: %w", err)
	}
	settlements, err := loadRules(ctx, tx, settlementRulesSQL)
	if err != nil {
		return nil, fmt.Errorf("read settlement rules: %w", err)
	}
	indexes := map[string]rules{"payments": payments, "settlements": settlements}
	entries, err := mapSettlement(ctx, tx, settlement, indexes)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("settlement %s has no amounts to map", settlement)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mapped_amounts WHERE settlement_id = $1", settlement); err != nil {
		return nil, fmt.Errorf("replace mapped amounts: %w", err)
	}
	if err := copyAmounts(ctx, tx, settlement, entries); err != nil {
		return nil, fmt.Errorf("write mapped amounts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mapped amounts: %w", err)
	}

	counts := make(map[string]int)
	for _, entry := range entries {
		counts[entry.status]++
	}
	return counts, nil
}

func loadRules(ctx context.Context, tx *sql.Tx, query string) (rules, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	index := make(rules)
	for rows.Next() {
		var match rule
		var transaction, field, description string
		if err := rows.Scan(&match.file, &match.line, &transaction, &field, &description,
			&match.template, &match.positive, &match.negative); err != nil {
			return nil, err
		}
		key := selector{normalize(transaction), normalize(field), normalize(description)}
		index[key] = append(index[key], match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(index) == 0 {
		return nil, fmt.Errorf("no rules loaded")
	}
	return index, nil
}

type entry struct {
	source, file string
	line         int
	mappedAmount
}

func mapSettlement(ctx context.Context, tx *sql.Tx, settlement string, indexes map[string]rules) ([]entry, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT source, source_file, source_line, raw_payload
FROM report_rows
WHERE settlement_id = $1 AND row_kind = 'detail'`, settlement)
	if err != nil {
		return nil, fmt.Errorf("read settlement details: %w", err)
	}
	defer rows.Close()

	var entries []entry
	for rows.Next() {
		row := reportRow{settlement: settlement}
		var payload []byte
		if err := rows.Scan(&row.source, &row.file, &row.line, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &row.payload); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", row.file, row.line, err)
		}
		amounts, err := mapRow(row, indexes[row.source])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", row.file, row.line, err)
		}
		for _, amount := range amounts {
			entries = append(entries, entry{row.source, row.file, row.line, amount})
		}
	}
	return entries, rows.Err()
}

func copyAmounts(ctx context.Context, tx *sql.Tx, settlement string, entries []entry) error {
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn("mapped_amounts",
		"settlement_id", "source", "source_file", "source_line", "amount_field", "amount_role",
		"amount", "order_id", "sku", "record_ref", "summary_bucket", "rule_table",
		"rule_file", "rule_line", "candidate_rules", "mapping_status", "mapping_issue"))
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, settlement, e.source, e.file, e.line, e.field, e.role,
			e.amount, e.order, e.sku, e.ref, e.bucket, e.ruleTable, e.ruleFile, e.ruleLine,
			pq.Array(e.candidates), e.status, e.issue); err != nil {
			return fmt.Errorf("%s:%d %s: %w", e.file, e.line, e.field, err)
		}
	}
	_, err = stmt.ExecContext(ctx)
	return err
}
