package main

import (
	"bufio"
	"context"
	"database/sql"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lib/pq"
)

//go:embed schema.sql
var schema string

type reportFile struct {
	name, source, settlementColumn string
	delimiter                      rune
	hasPreamble                    bool
}

var reports = []reportFile{
	{"amazon_payments_data.csv", "payments", "settlement ID", ',', true},
	{"amazon_settlements_data.txt", "settlements", "settlement-id", '\t', false},
}

type configFile struct {
	name, table string
	fields      []string
}

var configs = []configFile{
	{
		name:  "amazon_payment_configs_au_old.csv",
		table: "payment_mapping_rules",
		fields: []string{
			"transaction_type", "description", "amount_field", "record_ref",
			"to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount",
		},
	},
	{
		name:  "amazon_settlement_configs_au.csv",
		table: "settlement_mapping_rules",
		fields: []string{
			"transaction_type", "amount_type", "amount_description", "record_ref",
			"to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount",
		},
	},
}

func main() {
	dir := flag.String("reports", "reports", "directory containing Amazon reports and mapping CSVs")
	flag.Parse()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ingest(ctx, dsn, *dir); err != nil {
		log.Fatal(err)
	}
}

func ingest(ctx context.Context, dsn, dir string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ingestion transaction: %w", err)
	}
	defer tx.Rollback()

	// Serialize ingestion runs so one file replacement cannot race another.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(146379035, 1)"); err != nil {
		return fmt.Errorf("lock ingestion: %w", err)
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create ingestion tables: %w", err)
	}

	counts := make(map[string]int, len(reports)+len(configs))
	for _, input := range reports {
		count, err := loadReport(ctx, tx, dir, input)
		if err != nil {
			return fmt.Errorf("load %s: %w", input.name, err)
		}
		counts[input.name] = count
	}
	for _, input := range configs {
		count, err := loadConfig(ctx, tx, dir, input)
		if err != nil {
			return fmt.Errorf("load %s: %w", input.name, err)
		}
		counts[input.name] = count
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ingestion: %w", err)
	}
	for _, report := range reports {
		log.Printf("%s: %d rows loaded", report.name, counts[report.name])
	}
	for _, config := range configs {
		log.Printf("%s: %d rules loaded", config.name, counts[config.name])
	}
	return nil
}

func loadReport(ctx context.Context, tx *sql.Tx, dir string, input reportFile) (int, error) {
	if _, err := tx.ExecContext(ctx, "DELETE FROM report_rows WHERE source = $1 AND source_file = $2", input.source, input.name); err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn("report_rows",
		"source", "source_file", "source_line", "settlement_id", "row_kind", "raw_payload"))
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	required := []string{input.settlementColumn, "date/time", "type", "total"}
	if input.source == "settlements" {
		required = []string{input.settlementColumn, "transaction-type", "total-amount", "amount"}
	}
	count, err := readRows(filepath.Join(dir, input.name), input.delimiter, input.hasPreamble, required,
		func(line int, fields, values []string, positions map[string]int) error {
			settlementID := strings.TrimSpace(values[positions[input.settlementColumn]])
			if settlementID == "" {
				return errors.New("missing settlement ID")
			}
			kind := "detail"
			if input.source == "settlements" && values[positions["transaction-type"]] == "" &&
				values[positions["total-amount"]] != "" && values[positions["amount"]] == "" {
				kind = "settlement_header"
			}
			payload := make(map[string]string, len(fields))
			for i, field := range fields {
				payload[field] = values[i]
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			_, err = stmt.ExecContext(ctx, input.source, input.name, line, settlementID, kind, string(raw))
			return err
		})
	if err != nil {
		return 0, err
	}
	_, err = stmt.ExecContext(ctx)
	return count, err
}

func loadConfig(ctx context.Context, tx *sql.Tx, dir string, input configFile) (int, error) {
	deleteSQL := "DELETE FROM " + pq.QuoteIdentifier(input.table) + " WHERE source_file = $1"
	if _, err := tx.ExecContext(ctx, deleteSQL, input.name); err != nil {
		return 0, err
	}
	columns := append([]string{"source_file", "source_line"}, input.fields...)
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn(input.table, columns...))
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	count, err := readRows(filepath.Join(dir, input.name), ',', false, input.fields,
		func(line int, _ []string, values []string, positions map[string]int) error {
			args := []any{input.name, line}
			for _, field := range input.fields {
				args = append(args, values[positions[field]])
			}
			_, err := stmt.ExecContext(ctx, args...)
			return err
		})
	if err != nil {
		return 0, err
	}
	_, err = stmt.ExecContext(ctx)
	return count, err
}

func readRows(path string, delimiter rune, hasPreamble bool, required []string,
	visit func(line int, fields, values []string, positions map[string]int) error,
) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	buffer := bufio.NewReader(file)
	if prefix, _ := buffer.Peek(3); string(prefix) == "\uFEFF" {
		_, _ = buffer.Discard(3)
	}
	reader := csv.NewReader(buffer)
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1

	var fields []string
	var positions map[string]int
	for {
		fields, err = reader.Read()
		if errors.Is(err, io.EOF) {
			return 0, errors.New("header not found")
		}
		if err != nil {
			return 0, err
		}
		positions = make(map[string]int, len(fields))
		for index, field := range fields {
			positions[field] = index
		}
		missing := missingFields(positions, required)
		if len(missing) == 0 {
			break
		}
		if !hasPreamble {
			return 0, fmt.Errorf("header missing columns: %s", strings.Join(missing, ", "))
		}
	}
	if len(positions) != len(fields) {
		return 0, errors.New("header contains duplicate column names")
	}
	reader.FieldsPerRecord = len(fields)

	count := 0
	for {
		values, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
		line, _ := reader.FieldPos(0)
		if err := visit(line, fields, values, positions); err != nil {
			return 0, fmt.Errorf("line %d: %w", line, err)
		}
		count++
	}
	if count == 0 {
		return 0, errors.New("no data rows found")
	}
	return count, nil
}

func missingFields(positions map[string]int, required []string) []string {
	var missing []string
	for _, field := range required {
		if _, ok := positions[field]; !ok {
			missing = append(missing, field)
		}
	}
	return missing
}
