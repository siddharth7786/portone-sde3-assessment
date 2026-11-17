# PortOne Amazon reconciliation

Go and PostgreSQL reconcile the Amazon Payments and Settlement reports for settlement `12395580393`.

## Run the reconciliation

Use Docker Engine with Compose, Node.js 22+, and pnpm 11. Download the two original reports from the [assignment Drive folder](https://drive.google.com/drive/folders/10z6zVOfAztK9PBNjADYekdhMxkQb1The) and put them in `reports/` as `amazon_payments_data.csv` and `amazon_settlements_data.txt`. The mapping CSVs and sample workbook are included.

### Before the fixes

```sh
pnpm install --frozen-lockfile --ignore-scripts
docker compose run --rm --build ingest
docker compose run --rm mapper -settlement-id 12395580393
docker compose run -T --rm reconcile -settlement-id 12395580393 > /tmp/portone-before.json
node cmd/reconcile/workbook.mjs /tmp/portone-before.json \
  outputs/settlement-12395580393/before-fix.xlsx
```

### After the fixes

```sh
docker compose exec -T db psql -U portone -d portone -v ON_ERROR_STOP=1 < MAPPING_FIXES.sql
docker compose run --rm mapper -settlement-id 12395580393
docker compose run -T --rm reconcile -settlement-id 12395580393 -released-activity > /tmp/portone-after.json
node cmd/reconcile/workbook.mjs /tmp/portone-after.json \
  outputs/settlement-12395580393/after-fix.xlsx
```

Re-ingestion restores the original rules and removes derived mappings. Rerun the mapper after ingestion or rule changes.

## PostgreSQL schema

- `report_rows` holds both source reports. The settlement ID supports filtering, JSONB preserves their different layouts, and source, filename, and line number identify each original row.
- `payment_mapping_rules` and `settlement_mapping_rules` store the supplied configs separately so their source-specific fields and rule lines remain auditable.
- `mapped_amounts` stores one entry per processed amount with its key, bucket, status, and source and rule references.

## Assumptions

- Rules ignore case and separator differences. Exact transaction types take priority over blank fallbacks, and exact descriptions take priority over `any`. Ties and missing key fields are flagged.
- Key dates use UTC. Payments uses the release timestamp with the transaction timestamp as fallback. Settlement uses the posted timestamp with the posted date as fallback. `txn_ref` uses order ID, with merchant order ID and adjustment ID as Settlement fallbacks.
- Sum each source independently by `record_ref` and by bucket. Use a Payments row’s reported total only when component amounts are absent. A key that appears on both sides counts as reconciled.
- The after-fix Summary compares released settlement activity. Consolidated Data retains deferred activity and the bank transfer, and the Summary shows their effect on the raw difference. Combined taxes use the shared Tax category.

## Verified settlement results

| Measure | Result |
| --- | ---: |
| Reconciled keys | 13,289 |
| Payments-only groups | 2,382 |
| Settlement-only groups | 0 |
| Comparable Payments and Settlement totals, each | AUD 212,118.95 |
| Amazon reported settlement total | AUD 212,118.95 |
| Comparable key-and-category differences after fixes | 0 |

The Payments-only groups are 2,381 deferred groups and one bank transfer. See the [before-fix workbook](outputs/settlement-12395580393/before-fix.xlsx), [after-fix workbook](outputs/settlement-12395580393/after-fix.xlsx), and [variance breakdown](PROGRESS.md#before-fix-variance-by-summary-line).

## Restore the representative data sample

The [SQL sample](submission/representative.sql) contains the schema, original rules, Settlement header, and one matched order with location fields omitted. Use the full reports to reproduce the settlement totals.

```sh
docker compose up -d --wait db
docker compose exec -T db createdb -U portone portone_sample
docker compose exec -T db psql -U portone -d portone_sample -v ON_ERROR_STOP=1 < submission/representative.sql
```
