import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname } from 'node:path';

const require = createRequire(import.meta.url);

const [inputPath, outputPath, previewDir] = process.argv.slice(2);
if (!inputPath || !outputPath) {
  throw new Error('Usage: node workbook.mjs <reconcile.json> <output.xlsx> [preview-dir]');
}
const data = JSON.parse(await readFile(inputPath, 'utf8'));
// The portable writer is the default for a clean checkout. Preview runs use
// the bundled Artifact Tool to render, calculate, and inspect the workbook.
const artifact = previewDir ? require('@oai/artifact-tool') : null;
const workbook = artifact
  ? artifact.Workbook.create()
  : (await import('./portable-workbook.mjs')).createPortableWorkbook();
const summary = workbook.worksheets.add('Summary');
const detail = workbook.worksheets.add('Consolidated Data');
const issues = workbook.worksheets.add('Mapping Issues');
const money = '#,##0.00;[Red](#,##0.00);0.00';
const amount = value => value == null ? null : Number(value);

function setup(sheet, title, subtitle, widths) {
  sheet.showGridLines = false;
  widths.forEach((width, i) => sheet.getCell(0, i).format.columnWidthPx = width);
  sheet.getRangeByIndexes(0, 0, 1, widths.length).format = {
    fill: '#142D42', font: { color: '#FFFFFF', bold: true, size: 15 }, rowHeight: 29,
  };
  sheet.getCell(0, 0).values = [[title]];
  sheet.getRangeByIndexes(1, 0, 1, Math.min(widths.length, 5)).merge();
  sheet.getCell(1, 0).values = [[subtitle]];
  sheet.getCell(1, 0).format = { font: { color: '#36536C' }, rowHeight: 23 };
}

function header(sheet, headings) {
  const range = sheet.getRangeByIndexes(3, 0, 1, headings.length);
  range.values = [headings];
  range.format = { fill: '#25636C', font: { bold: true, color: '#FFFFFF' }, rowHeight: 23 };
  sheet.freezePanes.freezeRows(4);
}

function writeRows(sheet, rows) {
  for (let i = 0; i < rows.length; i += 1000) {
    const batch = rows.slice(i, i + 1000);
    sheet.getRangeByIndexes(4 + i, 0, batch.length, batch[0].length).values = batch;
  }
}

const releasedActivity = data.released_activity === true;
setup(summary,
  releasedActivity ? 'After-fix reconciliation' : 'Before-fix reconciliation',
  `Settlement ${data.settlement_id}  •  ${data.controls.currency}  •  ${releasedActivity ? 'Released activity vs. Settlement' : 'Original mapping rules'}`,
  [355, 132, 132, 210]);
header(summary, ['Summary line', 'Payments', 'Settlements', 'Payments − Settlements']);
const byBucket = new Map(data.summary.map(row => [row.summary_bucket, row]));
const sections = [
  ['Sales', [
    ['Product Charges', 'sales_product_charges'], ['Tax', 'sales_tax'],
    ['Shipping', 'sales_shipping'], ['Amazon Fees', 'sales_amazon_fees'],
    ['Inventory Reimbursements', 'sales_inventory_reimbursements'],
    ['Cross-account Debt Adjustment', 'sales_cross_account_debt_adjustment'],
    ['Other', 'sales_other'], ['FBA Fees', 'sales_fba_fees'],
    ['Micro Deposit (Failed)', 'sales_micro_deposit_failed'],
  ]],
  ['Refunds', [['Refund Expenses', 'refunded_expenses'], ['Refunded Sales', 'refunded_sales']]],
  ['Expenses', [
    ['Promo Rebates', 'expenses_promotional_rebates'], ['FBA Fees', 'expenses_fba_fees'],
    ['Cost of Advertising', 'expenses_cost_of_advertising'], ['Shipping Charges', 'expenses_shipping_charges'],
    ['Amazon Fees', 'expenses_amazon_fees'], ['Reversed Reimbursements', 'expenses_reversed_reimbursements'],
    ['Cross-account Debt Adjustment', 'expenses_cross_account_debt_adjustment'],
    ['Other', 'expenses_other'], ['Micro Deposit', 'bank_account_transfer_round_off'],
  ]],
];
let rowNumber = 5;
const subtotals = [];
function summaryRow(label, payments, settlements, showDifference = true) {
  const row = rowNumber++;
  summary.getRange(`A${row}:C${row}`).values = [[label, payments, settlements]];
  if (showDifference) summary.getCell(row - 1, 3).formulas = [[`=ROUND(B${row}-C${row},2)`]];
  return row;
}
function styleSubtotal(row) {
  summary.getRange(`A${row}:D${row}`).format = {
    fill: '#DDECEF', font: { bold: true, color: '#173F49' }, rowHeight: 19,
  };
}
function bucketRow(label, bucket) {
  const item = byBucket.get(bucket);
  byBucket.delete(bucket);
  return summaryRow(label, amount(item?.payments ?? '0'), amount(item?.settlements ?? '0'));
}
function section(label, buckets) {
  const row = summaryRow(label, null, null);
  subtotals.push(row);
  styleSubtotal(row);
  for (const [name, bucket] of buckets) bucketRow(name, bucket);
  for (const col of ['B', 'C']) {
    summary.getRange(`${col}${row}`).formulas = [[`=ROUND(SUM(${col}${row + 1}:${col}${rowNumber - 1}),2)`]];
  }
}
for (const [label, buckets] of sections) section(label, buckets);
subtotals.push(bucketRow('Paid To Amazon', 'paid_to_amazon'));
const additional = [...byBucket.keys()].filter(key => key !== null);
if (additional.length) {
  section('Additional configured buckets', additional.map(bucket => [bucket, bucket]));
}
const assigned = summaryRow('Total with a summary bucket', null, null);
for (const col of ['B', 'C']) {
  summary.getRange(`${col}${assigned}`).formulas = [[`=ROUND(SUM(${subtotals.map(row => col + row).join(',')}),2)`]];
}
styleSubtotal(assigned);
const unassigned = bucketRow('Amounts without a summary bucket', null);
const net = summaryRow(releasedActivity ? 'Comparable settlement activity net' : 'Net of included amounts', null, null);
for (const col of ['B', 'C']) {
  summary.getRange(`${col}${net}`).formulas = [[`=ROUND(${col}${assigned}+${col}${unassigned},2)`]];
}
styleSubtotal(net);
const raw = summaryRow('Net from raw source amounts', amount(data.controls.payment_file_total), amount(data.controls.settlement_file_total));
let scopeControl = raw;
if (releasedActivity) {
  const deferred = summaryRow('Deferred payments outside comparison', amount(data.controls.deferred_net), null, false);
  const transfer = summaryRow('Bank transfer outside comparison', amount(data.controls.transfer_net), null, false);
  scopeControl = summaryRow('Raw net after removing deferred and transfer', null, null);
  summary.getRange(`B${scopeControl}`).formulas = [[`=ROUND(B${raw}-B${deferred}-B${transfer},2)`]];
  summary.getRange(`C${scopeControl}`).formulas = [[`=C${raw}`]];
}
const coverage = summaryRow(releasedActivity ? 'Comparable net less scoped raw net' : 'Included net less raw source net', null, null);
for (const col of ['B', 'C']) {
  summary.getRange(`${col}${coverage}`).formulas = [[`=ROUND(${col}${net}-${col}${scopeControl},2)`]];
}
const reported = summaryRow('Amazon reported settlement total', null, amount(data.controls.reported_total), false);
const tie = summaryRow('Included net less Amazon reported total', null, null);
for (const col of ['B', 'C']) {
  summary.getRange(`${col}${tie}`).formulas = [[`=ROUND(${col}${net}-C${reported},2)`]];
}
summary.getRange(`B5:D${tie}`).format.numberFormat = money;

const countHeader = rowNumber + 1;
if (releasedActivity) {
  summary.getRange(`A${countHeader + 5}:B${countHeader + 5}`).values =
    [['Comparable key and bucket differences', data.comparable_bucket_variances]];
}
summary.getRange(`A${countHeader}:B${countHeader}`).values = [[releasedActivity ? 'Record matches across full input' : 'Record matches', 'Groups']];
styleSubtotal(countHeader);
const statuses = ['Reconciled', 'Payments only', 'Settlements only'];
summary.getRange(`A${countHeader + 1}:B${countHeader + statuses.length}`).values =
  statuses.map(status => [status, data.counts[status] ?? 0]);
summary.getRange(`B${countHeader + 1}:B${countHeader + statuses.length}`).format.numberFormat = '#,##0';

setup(detail, 'Consolidated Data',
  `Settlement ${data.settlement_id}  •  Source lines refer to amazon_payments_data.csv and amazon_settlements_data.txt.`,
  [420, 143, 112, 112, 112, 165, 210, 155, 260, 112, 112, 170, 220, 255, 170, 150, 278, 280]);
header(detail, ['Record reference', 'Status', 'Payments', 'Settlements', 'Difference', 'Order IDs', 'SKUs',
  'Transaction types', 'Amount fields and descriptions', 'Payments rows', 'Settlement rows',
  'Payments file lines', 'Settlement file lines', 'Summary buckets', 'Activity scope', 'Settlement ID', 'Payments source date', 'Settlement source date']);
writeRows(detail, data.consolidated.map(r => [
  r.record_ref, r.status, amount(r.payments), amount(r.settlements), amount(r.difference), r.order_ids, r.skus,
  r.transaction_types, r.amount_labels, r.payment_count, r.settlement_count, r.payment_lines, r.settlement_lines, r.buckets, r.activity ?? null, data.settlement_id, r.payment_dates, r.settlement_dates,
]));
detail.getRange(`C5:E${data.consolidated.length + 4}`).format.numberFormat = money;

setup(issues, 'Mapping Issues',
  `Settlement ${data.settlement_id}  •  Source rows and rule locations that need review.`,
  [115, 220, 104, 155, 105, 420, 168, 170, 275, 235, 90, 360]);
header(issues, ['Source', 'Source file', 'Source line', 'Amount field', 'Amount', 'Record reference',
  'Summary bucket', 'Mapping status', 'Issue', 'Rule file', 'Rule line', 'Candidate rules']);
writeRows(issues, data.issues.map(r => [
  r.source, r.source_file, r.source_line, r.amount_field, amount(r.amount), r.record_ref,
  r.summary_bucket, r.mapping_status, r.mapping_issue, r.rule_file, r.rule_line, r.candidate_rules,
]));
if (!data.issues.length) issues.getRange('A5:D5').merge();
if (!data.issues.length) issues.getRange('A5').values = [['No mapping issues in this run']];
if (data.issues.length) issues.getRange(`E5:E${data.issues.length + 4}`).format.numberFormat = money;

if (artifact) {
  workbook.recalculate();
  console.log((await workbook.inspect({
    kind: 'table', range: `Summary!A${assigned}:D${tie}`, include: 'values,formulas',
  })).ndjson);
  await mkdir(previewDir, { recursive: true });
  for (const [sheet, range] of [[summary, `A1:D${countHeader + (releasedActivity ? 5 : 3)}`], [detail, 'A1:G10'], [issues, 'A1:F10']]) {
    const preview = await workbook.render({ sheetName: sheet.name, range, scale: 1.25 });
    await writeFile(`${previewDir}/${sheet.name}.png`, new Uint8Array(await preview.arrayBuffer()));
  }
}
await mkdir(dirname(outputPath), { recursive: true });
if (artifact) {
  await (await artifact.SpreadsheetFile.exportXlsx(workbook)).save(outputPath);
} else {
  await workbook.save(outputPath);
}
