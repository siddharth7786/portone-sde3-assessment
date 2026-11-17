CREATE TABLE IF NOT EXISTS report_rows (
    source text NOT NULL CHECK (source IN ('payments', 'settlements')),
    source_file text NOT NULL,
    source_line integer NOT NULL CHECK (source_line > 0),
    settlement_id text NOT NULL CHECK (settlement_id <> ''),
    row_kind text NOT NULL CHECK (row_kind IN ('detail', 'settlement_header')),
    raw_payload jsonb NOT NULL,
    PRIMARY KEY (source, source_file, source_line),
    CHECK (row_kind = 'detail' OR source = 'settlements')
);

CREATE INDEX IF NOT EXISTS report_rows_settlement_source_idx
    ON report_rows (settlement_id, source);

CREATE TABLE IF NOT EXISTS payment_mapping_rules (
    source_file text NOT NULL,
    source_line integer NOT NULL CHECK (source_line > 0),
    transaction_type text NOT NULL,
    description text NOT NULL,
    amount_field text NOT NULL,
    record_ref text NOT NULL,
    to_summary_field_when_positive_amount text NOT NULL,
    to_summary_field_when_negative_amount text NOT NULL,
    PRIMARY KEY (source_file, source_line)
);

CREATE TABLE IF NOT EXISTS settlement_mapping_rules (
    source_file text NOT NULL,
    source_line integer NOT NULL CHECK (source_line > 0),
    transaction_type text NOT NULL,
    amount_type text NOT NULL,
    amount_description text NOT NULL,
    record_ref text NOT NULL,
    to_summary_field_when_positive_amount text NOT NULL,
    to_summary_field_when_negative_amount text NOT NULL,
    PRIMARY KEY (source_file, source_line)
);
