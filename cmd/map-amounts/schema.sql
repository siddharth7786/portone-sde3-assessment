CREATE TABLE IF NOT EXISTS mapped_amounts (
    settlement_id text NOT NULL,
    source text NOT NULL,
    source_file text NOT NULL,
    source_line integer NOT NULL,
    amount_field text NOT NULL,
    amount_role text NOT NULL CHECK (amount_role IN ('component', 'payment_total')),
    amount numeric NOT NULL,
    order_id text,
    sku text,
    record_ref text,
    summary_bucket text,
    rule_table text NOT NULL,
    rule_file text,
    rule_line integer,
    candidate_rules text[] NOT NULL DEFAULT '{}',
    mapping_status text NOT NULL CHECK (mapping_status IN (
        'mapped', 'no_summary_bucket', 'unmapped', 'ambiguous_rule', 'invalid_record_ref'
    )),
    mapping_issue text,
    PRIMARY KEY (source, source_file, source_line, amount_field),
    FOREIGN KEY (source, source_file, source_line)
        REFERENCES report_rows (source, source_file, source_line) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS mapped_amounts_settlement_record_ref_idx
    ON mapped_amounts (settlement_id, record_ref);
