WITH raw AS (
    SELECT source, source_file, source_line, row_kind, raw_payload
    FROM report_rows WHERE settlement_id = $1
), amounts AS (
    SELECT a.*, r.raw_payload,
           count(*) FILTER (WHERE a.amount_role = 'component') OVER (
               PARTITION BY a.source, a.source_file, a.source_line
           ) AS components
    FROM mapped_amounts a
    JOIN raw r USING (source, source_file, source_line)
    WHERE a.settlement_id = $1
), included AS (
    SELECT *,
           CASE WHEN source = 'payments' AND lower(trim(raw_payload->>'type')) = 'transfer'
                    THEN 'Bank transfer'
                WHEN source = 'payments' AND lower(trim(raw_payload->>'Transaction status')) = 'deferred'
                    THEN 'Deferred Payments'
                ELSE 'Settlement activity' END AS activity
    FROM amounts WHERE amount_role = 'component' OR components = 0
), refs AS (
    SELECT record_ref,
           CASE WHEN record_ref IS NULL THEN source END AS missing_source,
           CASE WHEN record_ref IS NULL THEN source_file END AS missing_file,
           CASE WHEN record_ref IS NULL THEN source_line END AS missing_line,
           CASE WHEN record_ref IS NULL THEN amount_field END AS missing_field,
           string_agg(DISTINCT activity, ', ' ORDER BY activity) AS activity,
           string_agg(DISTINCT nullif(order_id, ''), ', ') AS order_ids,
           string_agg(DISTINCT nullif(sku, ''), ', ') AS skus,
           string_agg(DISTINCT coalesce(raw_payload->>'type', raw_payload->>'transaction-type'), ', ') AS transaction_types,
           string_agg(DISTINCT CASE WHEN source = 'payments' THEN amount_field ELSE
               concat_ws(' / ', raw_payload->>'amount-type', raw_payload->>'amount-description') END, ', ') AS amount_labels,
           sum(amount) FILTER (WHERE source = 'payments') AS payments,
           sum(amount) FILTER (WHERE source = 'settlements') AS settlements,
           count(*) FILTER (WHERE source = 'payments') AS payment_count,
           count(*) FILTER (WHERE source = 'settlements') AS settlement_count,
           string_agg(DISTINCT source_line::text, ', ' ORDER BY source_line::text)
               FILTER (WHERE source = 'payments') AS payment_lines,
           string_agg(DISTINCT source_line::text, ', ' ORDER BY source_line::text)
               FILTER (WHERE source = 'settlements') AS settlement_lines,
           string_agg(DISTINCT summary_bucket, ', ' ORDER BY summary_bucket) AS buckets,
           string_agg(DISTINCT coalesce(nullif(raw_payload->>'Transaction Release Date', ''), raw_payload->>'date/time'), ', ')
               FILTER (WHERE source = 'payments') AS payment_dates,
           string_agg(DISTINCT coalesce(nullif(raw_payload->>'posted-date-time', ''), raw_payload->>'posted-date'), ', ')
               FILTER (WHERE source = 'settlements') AS settlement_dates
    FROM included
    GROUP BY 1, 2, 3, 4, 5
), consolidated AS (
    SELECT record_ref, activity, order_ids, skus, transaction_types, amount_labels,
           payments::text, settlements::text,
           (coalesce(payments, 0) - coalesce(settlements, 0))::text AS difference,
           payment_count, settlement_count, payment_lines, settlement_lines, buckets, payment_dates, settlement_dates,
           CASE WHEN payment_count > 0 AND settlement_count > 0 THEN 'Reconciled'
                WHEN payment_count > 0 THEN 'Payments only' ELSE 'Settlements only' END AS status,
           CASE WHEN payment_count > 0 AND settlement_count > 0 THEN 1
                WHEN payment_count > 0 THEN 2 ELSE 3 END AS sort_order,
           missing_line, missing_field
    FROM refs
), summary AS (
    SELECT summary_bucket,
           coalesce(sum(amount) FILTER (WHERE source = 'payments'), 0)::text AS payments,
           coalesce(sum(amount) FILTER (WHERE source = 'settlements'), 0)::text AS settlements
    FROM included
    WHERE NOT $2::boolean OR activity = 'Settlement activity'
    GROUP BY summary_bucket
), comparable_variances AS (
    SELECT record_ref, summary_bucket
    FROM included
    WHERE activity = 'Settlement activity'
    GROUP BY record_ref, summary_bucket
    HAVING sum(CASE WHEN source = 'payments' THEN amount ELSE -amount END) <> 0
), issues AS (
    SELECT source, source_file, source_line, amount_field, amount::text, record_ref,
           summary_bucket, mapping_status, mapping_issue, rule_file, rule_line,
           array_to_string(candidate_rules, ', ') AS candidate_rules
    FROM amounts
    WHERE mapping_issue IS NOT NULL
), controls AS (
    SELECT
        (SELECT sum(replace(raw_payload->>'total', ',', '')::numeric)::text
           FROM raw WHERE source = 'payments' AND row_kind = 'detail') AS payment_file_total,
        (SELECT sum(replace(raw_payload->>'amount', ',', '')::numeric)::text
           FROM raw WHERE source = 'settlements' AND row_kind = 'detail') AS settlement_file_total,
        (SELECT max(replace(raw_payload->>'total-amount', ',', '')::numeric)::text
           FROM raw WHERE row_kind = 'settlement_header') AS reported_total,
        (SELECT coalesce(sum(amount), 0)::text FROM included WHERE activity = 'Deferred Payments') AS deferred_net,
        (SELECT coalesce(sum(amount), 0)::text FROM included WHERE activity = 'Bank transfer') AS transfer_net,
        (SELECT max(raw_payload->>'currency')
           FROM raw WHERE row_kind = 'settlement_header') AS currency
)
SELECT jsonb_build_object(
    'settlement_id', $1::text,
    'released_activity', $2::boolean,
    'comparable_bucket_variances', (SELECT count(*) FROM comparable_variances),
    'summary', (SELECT jsonb_agg(to_jsonb(summary) ORDER BY summary_bucket) FROM summary),
    'consolidated', (SELECT jsonb_agg(to_jsonb(consolidated) ORDER BY sort_order, record_ref, missing_line, missing_field) FROM consolidated),
    'issues', (SELECT coalesce(jsonb_agg(to_jsonb(issues) ORDER BY source, source_line, amount_field), '[]') FROM issues),
    'controls', (SELECT to_jsonb(controls) FROM controls),
    'counts', (SELECT jsonb_object_agg(status, n) FROM (SELECT status, count(*) AS n FROM consolidated GROUP BY status) counts)
), (SELECT count(*) FROM raw WHERE row_kind = 'detail'
     AND (source = 'settlements' OR replace(raw_payload->>'total', ',', '')::numeric <> 0
          OR EXISTS (SELECT 1 FROM amounts a WHERE (a.source, a.source_file, a.source_line) = (raw.source, raw.source_file, raw.source_line)))),
   (SELECT count(DISTINCT (source, source_file, source_line)) FROM amounts),
   (SELECT count(*) FROM raw WHERE row_kind = 'settlement_header');
