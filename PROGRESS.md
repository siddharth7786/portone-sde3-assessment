# Settlement 12395580393 investigation

- Loaded the original files and mapping CSVs into PostgreSQL. Reconciled by key
  with the original rules and saved the baseline workbook. The net of the
  Payments file was AUD 121,902.51. Settlement detail and the Settlement header
  both showed AUD 212,118.95. The raw variance was −AUD 90,216.44.
- Split the baseline Product Charges variance of AUD 51,057.79 into
  AUD 64,629.60 of Payments-only product sales and −AUD 13,571.81 on matched
  keys. On matched keys, Payments and Settlement principal were both
  AUD 335,336.96. Settlement had also routed AUD 13,765.71 of collected tax and
  −AUD 193.90 of withheld tax to Product Charges.
- Grouped Payments-only keys by source transaction status and type. All
  2,381 keyed groups came from Deferred rows. Their Order total was
  AUD 43,693.89 and their Refund total was −AUD 153.82. The remaining
  Payments-only group was a released bank transfer of −AUD 133,756.51
  (Payments line 16605). Settlement detail contains no matching payout row.
  The raw-to-comparable bridge is
  `121902.51 - 43540.07 - (-133756.51) = 212118.95`.
- Matched the Promotions, GiftWrap, Tax, and Refund components by key.
  Matched Order rebates total −AUD 11,273.53 on both sides. Settlement
  GiftWrap contributed AUD 11.61 and Settlement GiftWrapTax contributed
  AUD 0.36 to baseline Sales Other. The Payments gift-wrap rule sent the same
  AUD 11.61 to a separate bucket. The tax was part of combined Payments tax.
- Verified the two ambiguous Payments selectors against the Settlement
  components. The 7,626 sales-tax amounts and 71 low-value-goods amounts
  came from duplicate selectors with conflicting buckets. On matched Orders,
  collected tax (AUD 13,675.59) includes the Settlement tax discount
  (−AUD 672.16), and withholding (−AUD 196.62) spans product and shipping.
  Used the sample's Tax line for the combined amounts. An independent
  Product Charges versus Shipping tax split would require component-level
  tax fields from Payments.
- Verified the matched Refund tax total of −AUD 42.59 as Settlement Refund
  Tax (−AUD 41.09), ShippingTax (−AUD 3.19) and TaxDiscount (AUD 1.69).
  Updated the rules to classify these as Refund Expenses.
- Kept the transfer amount and added a transfer-specific key rule for its
  `other` component. It now uses the same template as its Payments total.
  The after-fix comparison separates this cash movement and Deferred rows
  using original fields. No adjustment amount is hardcoded.
- Applied the SQL updates in Docker, reran the mapper and regenerated the
  after-fix workbook. Verified zero comparable key-and-bucket differences,
  zero mapping issues, and independently calculated totals of AUD 212,118.95
  on each side. Verified the exported workbook retains all 15,671 groups,
  including 2,381 Deferred groups and one bank transfer outside the comparison.

## Before-fix variance by summary line

Amounts are AUD. Differences are Payments minus Settlements. Payments-only
amounts are shown using the original buckets. Their contribution plus the
matched-key difference equals the baseline difference on each row.

| Summary line | Baseline difference | Payments-only contribution | Matched-key difference | Explanation |
| --- | ---: | ---: | ---: | --- |
| Product Charges | 51,057.79 | 64,629.60 | -13,571.81 | Deferred product sales plus Settlement product tax and withholding in Product Charges (rules 65 and 119). |
| Shipping | 4,322.91 | 4,229.71 | 93.20 | Deferred shipping plus the matched Settlement shipping tax, tax discounts and withholding routed to Shipping (rules 63, 95 and 118). |
| Sales Other | -11.97 | 0.00 | -11.97 | Settlement gift wrap and gift-wrap tax (rules 66 and 61). Payments used a separate gift-wrap bucket and had ambiguous combined tax. |
| Refund Expenses | 79.64 | 37.05 | 42.59 | Deferred refund rebates and selling fees, plus matched refund tax that Payments rule 14 left without a bucket. |
| Refunded Sales | -175.06 | -175.06 | 0.00 | Deferred product and shipping refunds from Payments. |
| Promo Rebates | 11,273.53 | 0.00 | 11,273.53 | Payments rule 65 routed these amounts to Expenses Other. |
| Expenses Amazon Fees | -27,108.53 | -27,108.53 | 0.00 | Deferred selling and fulfillment fees from Payments. |
| Expenses Other | -15,722.09 | -4,448.56 | -11,273.53 | Deferred and matched promotional rebates mapped here by Payments rule 65. |
| Additional gift-wrap bucket | 22.04 | 10.43 | 11.61 | Payments rule 70 used a separate bucket for the gift-wrap charge. |
| Amounts without a summary bucket | -113,954.70 | -127,391.08 | 13,436.38 | Ambiguous order tax and withholding, blank refund tax mappings, and the Payments-only bank transfer. |

The other category lines had zero baseline variance. After the fixes, every
comparable category matched, as did every comparable key-and-category pair.
Deferred activity and the transfer remain visible in Consolidated Data.
