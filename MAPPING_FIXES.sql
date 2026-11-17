BEGIN;
-- Serialize rule changes with ingestion and the mapper.
SELECT pg_advisory_xact_lock(146379035, 1);

-- ORDER promotional_rebates (Payments line 65): Other -> Promo Rebates.
-- Matched Payments rebates total -11,273.53, exactly Settlement Promotion
-- Principal and Shipping (lines 72–73). TaxDiscount is part of Payments tax.
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'expenses_promotional_rebates',
    to_summary_field_when_negative_amount = 'expenses_promotional_rebates'
WHERE transaction_type = 'ORDER' AND description = 'any' AND amount_field = 'promotional_rebates';

-- ORDER gift_wrap_credits (Payments line 70): dedicated bucket -> Sales Other.
-- Settlement GiftWrap (line 66) matches all three corresponding credits, 11.61.
-- An additional 10.43 of credits belongs to Deferred Payments. GiftWrapTax
-- moves to Tax below since Payments includes it in sales_tax_collected.
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_other',
    to_summary_field_when_negative_amount = 'sales_other'
WHERE transaction_type = 'ORDER' AND description = 'any' AND amount_field = 'gift_wrap_credits';

-- ORDER tax (Payments lines 71–72): remove the Shipping duplicate and move
-- the Product Charges rule to Tax. Matched Payments tax of 13,675.59 combines
-- Settlement Tax, ShippingTax, GiftWrapTax and Promotion/TaxDiscount.
DELETE FROM payment_mapping_rules
WHERE transaction_type = 'ORDER' AND description = 'any' AND amount_field = 'sales_tax_collected'
  AND to_summary_field_when_positive_amount = 'sales_shipping'
  AND to_summary_field_when_negative_amount = 'sales_shipping';
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE transaction_type = 'ORDER' AND description = 'any' AND amount_field = 'sales_tax_collected';
UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE transaction_type = 'ORDER'
  AND ((amount_type = 'ITEMPRICE' AND amount_description IN ('TAX', 'SHIPPINGTAX', 'GIFTWRAPTAX'))
       OR (amount_type = 'PROMOTION' AND amount_description = 'TAXDISCOUNT'));

-- ORDER withheld tax (Payments lines 5–6): remove the Shipping duplicate and
-- move the Product Charges rule to Tax. Matched Payments -196.62 combines
-- Settlement -193.90 principal and -2.72 shipping withholding. Both are tax
-- components. The Payments CSV lacks the detail to reproduce that split.
DELETE FROM payment_mapping_rules
WHERE transaction_type = 'ORDER' AND description = 'any' AND amount_field = 'low_value_goods'
  AND to_summary_field_when_positive_amount = 'sales_shipping'
  AND to_summary_field_when_negative_amount = 'sales_shipping';
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE transaction_type = 'ORDER' AND description = 'any' AND amount_field = 'low_value_goods';
UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE transaction_type = 'ORDER' AND amount_type = 'ITEMWITHHELDTAX'
  AND amount_description IN ('LOWVALUEGOODSTAX-PRINCIPAL', 'LOWVALUEGOODSTAX-SHIPPING', 'LOWVALUEGOODSTAX-OTHER');

-- REFUND sales_tax_collected (Payments line 14): blank -> Refund Expenses.
-- These matched amounts total -42.59, the sum of Settlement Refund Tax,
-- ShippingTax and Promotion/TaxDiscount. Keep both signs in Refund Expenses
-- for those Settlement rules so reversals stay in the same category.
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'refunded_expenses',
    to_summary_field_when_negative_amount = 'refunded_expenses'
WHERE transaction_type = 'REFUND' AND description = 'any' AND amount_field = 'sales_tax_collected';
UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'refunded_expenses',
    to_summary_field_when_negative_amount = 'refunded_expenses'
WHERE transaction_type = 'REFUND'
  AND ((amount_type = 'ITEMPRICE' AND amount_description IN ('TAX', 'SHIPPINGTAX'))
       OR (amount_type = 'PROMOTION' AND amount_description = 'TAXDISCOUNT'));

-- Bank transfer (Payments line 16605): the other component previously fell
-- through to an order-ID template despite having no order ID. Use the same
-- key and blank sales bucket as the supplied transfer total rules (62–63).
INSERT INTO payment_mapping_rules (
    source_file, source_line, transaction_type, description, amount_field, record_ref,
    to_summary_field_when_positive_amount, to_summary_field_when_negative_amount
)
SELECT source_file, source_line + 1000, transaction_type, description, 'other', record_ref, '', ''
FROM payment_mapping_rules
WHERE transaction_type = 'TRANSFER' AND amount_field = 'total'
  AND description IN ('TO_ACCOUNT_ENDING', 'TO_YOUR_ACCOUNT_ENDING')
ON CONFLICT (source_file, source_line) DO NOTHING;

-- Deferred activity and the cash transfer lack Settlement-side detail.
-- Reporting scope comes from the source status and type. See README and PROGRESS.md.
COMMIT;
