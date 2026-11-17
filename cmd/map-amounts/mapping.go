package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type rule struct {
	file                         string
	line                         int
	template, positive, negative string
}

type selector struct {
	transaction, field, description string
}

type rules map[selector][]rule

func normalize(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == '_' {
			return -1
		}
		return unicode.ToUpper(r)
	}, value)
}

func (r rules) match(transaction, field, description string) []rule {
	for _, txn := range []string{normalize(transaction), ""} {
		for _, desc := range []string{normalize(description), "ANY"} {
			if candidates := r[selector{txn, normalize(field), desc}]; len(candidates) > 0 {
				return candidates
			}
		}
	}
	return nil
}

type amountColumn struct {
	header, field string
}

var paymentColumns = []amountColumn{
	{"product sales", "product_sales"},
	{"shipping credits", "shipping_credits"},
	{"gift wrap credits", "gift_wrap_credits"},
	{"promotional rebates", "promotional_rebates"},
	{"sales tax collected", "sales_tax_collected"},
	{"low value goods", "low_value_goods"},
	{"selling fees", "selling_fees"},
	{"fulfilment by amazon fees", "fba_fees"},
	{"other transaction fees", "other_transaction_fees"},
	{"other", "other"},
	{"marketplace withheld tax", "marketplace_withheld_tax"},
	{"total", "total"},
}

type reportRow struct {
	settlement, source, file string
	line                     int
	payload                  map[string]string
}

type mappedAmount struct {
	amount, field, role, order, sku, ruleTable string
	ref, bucket, ruleFile, issue               *string
	ruleLine                                   *int
	candidates                                 []string
	status                                     string
}

var decimal = regexp.MustCompile(`^[+-]?(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?$`)

func parseAmount(raw string) (amount string, sign int, err error) {
	raw = strings.TrimSpace(raw)
	if !decimal.MatchString(raw) {
		return "", 0, fmt.Errorf("invalid amount %q", raw)
	}
	amount = strings.ReplaceAll(raw, ",", "")
	if strings.Trim(amount, "+-0.") == "" {
		return amount, 0, nil
	}
	if strings.HasPrefix(amount, "-") {
		return amount, -1, nil
	}
	return amount, 1, nil
}

func paymentDescription(transaction, description string) string {
	// The transfer rules describe the prefix before the masked bank account.
	if normalize(transaction) == "TRANSFER" {
		before, _, found := strings.Cut(description, ":")
		if found {
			switch normalize(before) {
			case "TOACCOUNTENDING", "TOACCOUNTENDINGWITH":
				return "TO_ACCOUNT_ENDING"
			case "TOYOURACCOUNTENDING", "TOYOURACCOUNTENDINGWITH":
				return "TO_YOUR_ACCOUNT_ENDING"
			}
		}
	}
	return description
}

func mapRow(row reportRow, index rules) ([]mappedAmount, error) {
	p := row.payload
	if row.source == "settlements" {
		amount, sign, err := parseAmount(p["amount"])
		if err != nil {
			return nil, err
		}
		candidates := index.match(p["transaction-type"], p["amount-type"], p["amount-description"])
		mapped := mapAmount(row, "amount", amount, sign, candidates)
		return []mappedAmount{mapped}, nil
	}

	var amounts []mappedAmount
	description := paymentDescription(p["type"], p["description"])
	for _, column := range paymentColumns {
		raw := p[column.header]
		if strings.TrimSpace(raw) == "" {
			continue
		}
		amount, sign, err := parseAmount(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", column.header, err)
		}
		if sign == 0 {
			continue
		}
		candidates := index.match(p["type"], column.field, description)
		amounts = append(amounts, mapAmount(row, column.field, amount, sign, candidates))
	}
	return amounts, nil
}

func mapAmount(row reportRow, field, amount string, sign int, candidates []rule) mappedAmount {
	result := mappedAmount{
		amount: amount, field: field, role: "component",
		order: row.orderID(), sku: row.payload["sku"],
		ruleTable: "settlement_mapping_rules", candidates: []string{},
	}
	if row.source == "payments" {
		result.ruleTable = "payment_mapping_rules"
		if field == "total" {
			result.role = "payment_total"
		}
	}

	switch len(candidates) {
	case 0:
		result.status = "unmapped"
		message := "no matching rule"
		result.issue = &message
	case 1:
		match := candidates[0]
		result.ruleFile, result.ruleLine = &match.file, &match.line
		result.status = "no_summary_bucket"
		bucket := ""
		switch sign {
		case 1:
			bucket = match.positive
		case -1:
			bucket = match.negative
		}
		if bucket != "" {
			result.bucket = &bucket
			result.status = "mapped"
		}
		ref, err := row.recordRef(match.template)
		if err != nil {
			result.status = "invalid_record_ref"
			message := err.Error()
			result.issue = &message
		} else {
			result.ref = &ref
		}
	default:
		result.status = "ambiguous_rule"
		message := "multiple rules have the same precedence"
		result.issue = &message
		template := candidates[0].template
		for _, match := range candidates {
			result.candidates = append(result.candidates, fmt.Sprintf("%s:%d", match.file, match.line))
			if match.template != template {
				template = ""
			}
		}
		if ref, err := row.recordRef(template); err == nil {
			result.ref = &ref
		}
	}
	return result
}

func (r reportRow) orderID() string {
	if r.source == "payments" {
		return r.payload["order ID"]
	}
	return r.payload["order-id"]
}

func (r reportRow) recordRef(template string) (string, error) {
	if strings.TrimSpace(template) == "" {
		return "", fmt.Errorf("rule has no record_ref template")
	}
	p := r.payload
	txnRef := strings.TrimSpace(r.orderID())
	if txnRef == "" && r.source == "settlements" {
		txnRef = strings.TrimSpace(p["merchant-order-id"])
		if txnRef == "" {
			txnRef = strings.TrimSpace(p["adjustment-id"])
		}
	}
	description, transaction := p["description"], p["type"]
	if r.source == "settlements" {
		description, transaction = p["amount-description"], p["transaction-type"]
	}
	values := map[string]string{
		"txn_ref": txnRef, "sku": p["sku"], "settlement_id": r.settlement,
		"merchant_order_id": p["merchant-order-id"], "shipment_id": p["shipment-id"],
		"description": description, "record_type": normalize(transaction),
	}
	parts := strings.Split(template, "+")
	escape := strings.NewReplacer("%", "%25", "+", "%2B")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "date" {
			date, err := r.date()
			if err != nil {
				return "", err
			}
			parts[i] = date
			continue
		}
		if value, ok := values[part]; ok {
			part = strings.TrimSpace(value)
			if part == "" {
				return "", fmt.Errorf("missing value for %s", parts[i])
			}
		}
		if part == "" {
			return "", fmt.Errorf("empty record_ref template part")
		}
		parts[i] = escape.Replace(part)
	}
	return strings.Join(parts, "+"), nil
}

func (r reportRow) date() (string, error) {
	p := r.payload
	var timestamp time.Time
	var err error
	if r.source == "payments" {
		raw := p["Transaction Release Date"]
		if strings.TrimSpace(raw) == "" {
			raw = p["date/time"]
		}
		clock, offset, found := strings.Cut(strings.TrimSpace(raw), " GMT")
		hours, offsetErr := strconv.Atoi(offset)
		if !found || offsetErr != nil || hours < -23 || hours > 23 {
			return "", fmt.Errorf("invalid Payments timestamp %q", raw)
		}
		zone := time.FixedZone("GMT", hours*3600)
		timestamp, err = time.ParseInLocation("2 January 2006 3:04:05 pm", clock, zone)
	} else if raw := strings.TrimSpace(p["posted-date-time"]); raw != "" {
		timestamp, err = time.Parse("02.01.2006 15:04:05 UTC", raw)
	} else {
		timestamp, err = time.Parse("02.01.2006", p["posted-date"])
	}
	if err != nil {
		return "", fmt.Errorf("invalid report date: %w", err)
	}
	return timestamp.UTC().Format(time.DateOnly), nil
}
