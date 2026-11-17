# PortOne Amazon reconciliation

## Run ingestion with Docker

Get the two original Amazon reports from the [assignment Drive folder](https://drive.google.com/drive/folders/10z6zVOfAztK9PBNjADYekdhMxkQb1The). Place the supplied files in `reports/` using their original names:

- `amazon_payments_data.csv`
- `amazon_settlements_data.txt`
- `amazon_payment_configs_au_old.csv`
- `amazon_settlement_configs_au.csv`

The two mapping CSVs and sample Excel template are included in the repository. Use Docker Engine and Docker Compose for PostgreSQL and Go. Use Node.js 22 or newer and pnpm 11 for Excel export.

```sh
pnpm install --frozen-lockfile --ignore-scripts
docker compose run --rm --build ingest
```

Compose starts PostgreSQL, waits for it to accept TCP connections, then runs the Go loader. PostgreSQL uses a named volume and the loader mounts the reports read-only.

Check the loaded data:

```sh
docker compose exec -T db psql -U portone -d portone <<'SQL'
SELECT source, row_kind, count(*) FROM report_rows GROUP BY 1, 2 ORDER BY 1, 2;
SELECT settlement_id, count(*) FROM report_rows WHERE source = 'payments' GROUP BY 1 ORDER BY 1;
SELECT count(*) FROM payment_mapping_rules;
SELECT count(*) FROM settlement_mapping_rules;
SQL
```

The supplied files should give 23,026 Payments details, 54,979 Settlement details, one Settlement header, 149 Payments mapping rules, and 137 Settlement mapping rules. All four Payments settlement IDs are loaded. Settlement `12395580393` has 15,870 Payments rows.

## Ingestion schema

- `report_rows` stores both reports. The source, filename, and original starting line identify a row. The searchable settlement ID is stored alongside the parsed header/value pairs in `raw_payload` (JSONB). Values are kept as strings to preserve their source representation. `row_kind` identifies the Settlement metadata row, which contains Amazon's reported total.
- `payment_mapping_rules` and `settlement_mapping_rules` retain the source CSV columns and line numbers. Duplicate rule values and blank fields are preserved for investigation.

Each run replaces the data for the four input filenames in one transaction. Running ingestion again restores the supplied configs, so apply any future SQL fixes after ingestion. A failed load rolls back all four replacements.

Run `docker compose down` to stop PostgreSQL. The named volume persists.

## Map a settlement

After ingestion, run:

```sh
docker compose run --rm --build mapper -settlement-id 12395580393
```

The mapper reads the rules currently in PostgreSQL and replaces this settlement's
`mapped_amounts` rows in one transaction. Re-ingesting the raw reports removes
their derived mappings, so run the mapper again after ingestion or rule changes.

In DBCode, inspect a known order:

```sql
SELECT source, source_line, amount_field, amount, record_ref, summary_bucket,
       rule_line, mapping_status
FROM mapped_amounts
WHERE settlement_id = '12395580393'
  AND order_id = '249-3364825-5895055'
ORDER BY source, source_line, amount_field;
```

- Each nonzero Payments amount and each Settlement detail amount gets a row.
  Payments `total` is labeled `payment_total` so it can be distinguished from
  its components in subsequent aggregation.
- Matching ignores case and differences in spaces, hyphens, and underscores.
  Exact transaction types take precedence over blank transaction types. Within
  either group, exact descriptions take precedence over `any`. The mapper
  recognizes the masked bank-account description format in Payments transfer
  rows. It keeps the original description for key construction.
- The key uses the UTC calendar date of the Payments release timestamp
  (transaction timestamp if release is blank) or Settlement posted timestamp
  (posted date if the timestamp is blank). `txn_ref` uses the order ID, with
  merchant order ID and adjustment ID as Settlement fallbacks.
- `no_summary_bucket` means the rule chose a blank bucket or the amount was
  zero. `unmapped`, `ambiguous_rule`, and `invalid_record_ref` preserve the
  amount and expose the issue. A tie gets no bucket or selected rule, and the
  candidate rule locations remain visible. If tied rules share a resolvable key
  template, that key is still shown.

## Generate the before-fix workbook

Run after ingestion and mapping, with the original rules loaded:

```sh
docker compose build reconcile
docker compose run -T --rm reconcile -settlement-id 12395580393 > /tmp/portone-reconcile.json
```

The Go command queries one PostgreSQL snapshot and exports the reconciliation
as JSON. Generate the workbook with the pinned ExcelJS dependency. Excel
recalculates the Summary formulas on open.

```sh
node cmd/reconcile/workbook.mjs \
  /tmp/portone-reconcile.json outputs/settlement-12395580393/before-fix.xlsx
```

Consolidated Data groups component amounts by `record_ref` and reports a matched
key even if its amounts differ. A Payments total supplies the amount only when
its source row has no components. An amount with a missing key stays on its
source side as its own group and appears on Mapping Issues. The two reports
are summed independently in Summary. Buckets without a corresponding sample
line remain visible under Additional configured buckets. The raw source totals
and the header's reported total are separate controls.

## Apply the mapping fixes and regenerate

Generate the before-fix workbook first if starting from fresh data. The SQL
changes the loaded rule tables. Re-ingestion restores the original rules.

```sh
docker compose build reconcile
docker compose exec -T db psql -U portone -d portone -v ON_ERROR_STOP=1 < MAPPING_FIXES.sql
docker compose run -T --rm mapper -settlement-id 12395580393
docker compose run -T --rm reconcile -settlement-id 12395580393 -released-activity > /tmp/portone-after.json
node cmd/reconcile/workbook.mjs \
  /tmp/portone-after.json outputs/settlement-12395580393/after-fix.xlsx
```

The after-fix Summary compares released settlement activity. Deferred payments
and the bank transfer remain in Consolidated Data, with their scope labeled.
The Summary shows how those amounts bridge the raw Payments net to the
comparable net. Match counts still cover the full input. Tax is reported on a
shared Tax line because Payments combines product, shipping and gift-wrap tax.
See [PROGRESS.md](PROGRESS.md) for the source evidence.

## Verified settlement results

| Measure | Result |
| --- | ---: |
| Reconciled keys | 13,289 |
| Payments-only groups | 2,382 |
| Settlement-only groups | 0 |
| Comparable Payments and Settlement totals (each) | AUD 212,118.95 |
| Amazon reported settlement total | AUD 212,118.95 |
| Comparable key-and-category differences after fixes | 0 |

The Payments-only groups consist of 2,381 deferred groups and one bank
transfer. See the [before-fix workbook](outputs/settlement-12395580393/before-fix.xlsx),
[after-fix workbook](outputs/settlement-12395580393/after-fix.xlsx), and
[line-by-line variance analysis](PROGRESS.md#before-fix-variance-by-summary-line).

## Restore the representative data sample

[`submission/representative.sql`](submission/representative.sql) contains
the ingestion schema, all original mapping rules, the Settlement header,
and the Payments and Settlement detail for one matched order. The sample
omits the order location fields. The header retains the original batch total.
Use both full source files for a complete tie-out.

Restore the sample into its own empty database:

```sh
docker compose up -d --wait db
docker compose exec -T db createdb -U portone portone_sample
docker compose exec -T db psql -U portone -d portone_sample -v ON_ERROR_STOP=1   < submission/representative.sql
```
