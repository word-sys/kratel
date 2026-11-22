package num

import (
	"strings"
	"unicode"
)

// LineValuation computes the exact fixed-point value of a stock line:
// round((quantityMillis * unitCostMillis) / 1000).
func LineValuation(quantityMillis, unitCostMillis Millis) (Millis, error) {
	return quantityMillis.MulMillis(unitCostMillis)
}

// LineValuationSafe computes the non-negative inventory valuation for a stock balance.
// Negative stock balances (when negative stock is permitted) contribute 0 to positive asset valuation.
func LineValuationSafe(quantityMillis, unitCostMillis Millis) Millis {
	if quantityMillis <= 0 || unitCostMillis <= 0 {
		return 0
	}
	val, err := quantityMillis.MulMillis(unitCostMillis)
	if err != nil {
		return 0
	}
	return val
}

// SumValuations accumulates multiple Millis amounts using CheckedAdd to prevent overflow.
func SumValuations(amounts ...Millis) (Millis, error) {
	var total Millis
	for _, a := range amounts {
		next, err := total.CheckedAdd(a)
		if err != nil {
			return 0, err
		}
		total = next
	}
	return total, nil
}

// FormatMoney formats a Millis monetary amount in a region-agnostic manner.
// It uses 2 decimal places by default ("12.50"), or 3 decimal places ("0.005") when
// sub-cent thousandth precision is non-zero (common for bulk electronic passives & fasteners).
// If currency is empty, only the number is returned. If currency is an alphabetic code
// (e.g. "TRY", "EUR", "USD"), it is appended with a space; symbol characters (e.g. "₺", "€", "$")
// are placed immediately after any negative sign.
func FormatMoney(amount Millis, currency string) string {
	cur := strings.TrimSpace(currency)
	neg := amount < 0
	absAmt := amount.Abs()

	decimals := 2
	if absAmt.FractionalMillis()%10 != 0 {
		decimals = 3
	}
	numStr := absAmt.FormatFixed(decimals)

	if cur == "" {
		if neg && absAmt > 0 {
			return "-" + numStr
		}
		return numStr
	}

	if isAlphaCode(cur) {
		if neg && absAmt > 0 {
			return "-" + numStr + " " + cur
		}
		return numStr + " " + cur
	}

	if neg && absAmt > 0 {
		return "-" + cur + numStr
	}
	return cur + numStr
}

func isAlphaCode(s string) bool {
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
