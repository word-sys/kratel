package num

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
)

// ParseMillis parses a decimal string into exact fixed-point Millis (1000 == 1.000)
// without intermediate IEEE-754 floating-point conversion.
// It supports standard dot decimals ("12.345"), regional comma decimals ("12,345"),
// leading/trailing signs ("+5", "-0.25"), leading decimal points (".5"), and
// formatted thousands separators ("1,250.75" or "1.250,75").
func ParseMillis(raw string) (Millis, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u00a0", "") // non-breaking space
	if s == "" {
		return 0, apperr.Validation("quantity", "numeric value cannot be empty")
	}

	sign := int64(1)
	if s[0] == '+' {
		s = s[1:]
	} else if s[0] == '-' {
		sign = -1
		s = s[1:]
	}
	if s == "" {
		return 0, apperr.Validation("quantity", fmt.Sprintf("invalid numeric value %q", raw))
	}

	intPartStr, fracPartStr, err := splitDecimalParts(s, raw)
	if err != nil {
		return 0, err
	}

	if intPartStr == "" && fracPartStr == "" {
		return 0, apperr.Validation("quantity", fmt.Sprintf("invalid numeric value %q", raw))
	}

	var wholeUnits int64
	if intPartStr != "" {
		for i := 0; i < len(intPartStr); i++ {
			if intPartStr[i] < '0' || intPartStr[i] > '9' {
				return 0, apperr.Validation("quantity", fmt.Sprintf("invalid numeric value %q", raw))
			}
		}
		parsedWhole, err := strconv.ParseInt(intPartStr, 10, 64)
		if err != nil || parsedWhole > MaxSafeMillis/Scale {
			return 0, apperr.Validation("quantity", fmt.Sprintf("numeric value %q exceeds safe bounds", raw))
		}
		wholeUnits = parsedWhole
	}

	for i := 0; i < len(fracPartStr); i++ {
		if fracPartStr[i] < '0' || fracPartStr[i] > '9' {
			return 0, apperr.Validation("quantity", fmt.Sprintf("invalid fractional digits in %q", raw))
		}
	}

	var fracMillis int64
	switch len(fracPartStr) {
	case 0:
		fracMillis = 0
	case 1:
		fracMillis = int64(fracPartStr[0]-'0') * 100
	case 2:
		fracMillis = int64(fracPartStr[0]-'0')*100 + int64(fracPartStr[1]-'0')*10
	case 3:
		fracMillis = int64(fracPartStr[0]-'0')*100 + int64(fracPartStr[1]-'0')*10 + int64(fracPartStr[2]-'0')
	default:
		fracMillis = int64(fracPartStr[0]-'0')*100 + int64(fracPartStr[1]-'0')*10 + int64(fracPartStr[2]-'0')
		if fracPartStr[3] >= '5' {
			fracMillis++
		}
	}

	totalMillis := wholeUnits*Scale + fracMillis
	if totalMillis > MaxSafeMillis {
		return 0, apperr.Validation("quantity", fmt.Sprintf("numeric value %q exceeds maximum supported magnitude", raw))
	}
	return Millis(sign * totalMillis), nil
}

// splitDecimalParts normalizes US/UK ("1,234.56") and European/Turkish ("1.234,56")
// decimal and thousands separators into clean integer and fractional digit strings.
func splitDecimalParts(s, raw string) (string, string, error) {
	dotCount := strings.Count(s, ".")
	commaCount := strings.Count(s, ",")

	switch {
	case dotCount == 0 && commaCount == 0:
		return s, "", nil

	case dotCount > 0 && commaCount > 0:
		lastDot := strings.LastIndexByte(s, '.')
		lastComma := strings.LastIndexByte(s, ',')
		if lastDot > lastComma {
			// Dot is the decimal separator; commas are thousands separators ("1,250.75")
			if dotCount > 1 {
				return "", "", apperr.Validation("quantity", fmt.Sprintf("malformed decimal separators in %q", raw))
			}
			cleaned := strings.ReplaceAll(s[:lastDot], ",", "")
			if strings.Contains(s[lastDot+1:], ",") {
				return "", "", apperr.Validation("quantity", fmt.Sprintf("malformed decimal separators in %q", raw))
			}
			return cleaned, s[lastDot+1:], nil
		}
		// Comma is the decimal separator; dots are thousands separators ("1.250,75")
		if commaCount > 1 {
			return "", "", apperr.Validation("quantity", fmt.Sprintf("malformed decimal separators in %q", raw))
		}
		cleaned := strings.ReplaceAll(s[:lastComma], ".", "")
		if strings.Contains(s[lastComma+1:], ".") {
			return "", "", apperr.Validation("quantity", fmt.Sprintf("malformed decimal separators in %q", raw))
		}
		return cleaned, s[lastComma+1:], nil

	case dotCount == 1 && commaCount == 0:
		idx := strings.IndexByte(s, '.')
		return s[:idx], s[idx+1:], nil

	case commaCount == 1 && dotCount == 0:
		idx := strings.IndexByte(s, ',')
		return s[:idx], s[idx+1:], nil

	case dotCount > 1 && commaCount == 0:
		if isValidThousandsGrouping(s, '.') {
			return strings.ReplaceAll(s, ".", ""), "", nil
		}
		return "", "", apperr.Validation("quantity", fmt.Sprintf("multiple decimal points in %q", raw))

	case commaCount > 1 && dotCount == 0:
		if isValidThousandsGrouping(s, ',') {
			return strings.ReplaceAll(s, ",", ""), "", nil
		}
		return "", "", apperr.Validation("quantity", fmt.Sprintf("multiple comma separators in %q", raw))
	}

	return "", "", apperr.Validation("quantity", fmt.Sprintf("invalid numeric value %q", raw))
}

func isValidThousandsGrouping(s string, sep byte) bool {
	parts := strings.Split(s, string(sep))
	if len(parts) < 2 || len(parts[0]) == 0 || len(parts[0]) > 3 {
		return false
	}
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) != 3 {
			return false
		}
	}
	return true
}

// ParsePositiveMillis parses raw into Millis and validates that the value is strictly > 0.
func ParsePositiveMillis(field, raw string) (Millis, error) {
	m, err := ParseMillis(raw)
	if err != nil {
		if domErr, ok := err.(*apperr.Error); ok {
			return 0, domErr.WithField(field)
		}
		return 0, apperr.Validation(field, err.Error())
	}
	if m <= 0 {
		return 0, apperr.Validation(field, fmt.Sprintf("%s must be greater than zero", field))
	}
	return m, nil
}

// ParseNonNegativeMillis parses raw into Millis and validates that the value is >= 0.
func ParseNonNegativeMillis(field, raw string) (Millis, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	m, err := ParseMillis(raw)
	if err != nil {
		if domErr, ok := err.(*apperr.Error); ok {
			return 0, domErr.WithField(field)
		}
		return 0, apperr.Validation(field, err.Error())
	}
	if m < 0 {
		return 0, apperr.Validation(field, fmt.Sprintf("%s cannot be negative", field))
	}
	return m, nil
}

// Format returns a human-friendly decimal string with trailing fractional zeros stripped
// (e.g., 2000 -> "2", 1500 -> "1.5", 125 -> "0.125", -50 -> "-0.05").
func (m Millis) Format() string {
	if m == 0 {
		return "0"
	}
	neg := m < 0
	absVal := int64(m)
	if neg {
		absVal = -absVal
	}
	whole := absVal / Scale
	frac := absVal % Scale

	prefix := ""
	if neg {
		prefix = "-"
	}
	if frac == 0 {
		return fmt.Sprintf("%s%d", prefix, whole)
	}
	fracStr := fmt.Sprintf("%03d", frac)
	fracStr = strings.TrimRight(fracStr, "0")
	return fmt.Sprintf("%s%d.%s", prefix, whole, fracStr)
}

// FormatFixed formats Millis with an exact number of decimal places (0, 1, 2, or 3).
func (m Millis) FormatFixed(decimals int) string {
	neg := m < 0
	absVal := int64(m)
	if neg {
		absVal = -absVal
	}
	prefix := ""
	if neg && absVal > 0 {
		prefix = "-"
	}

	switch {
	case decimals <= 0:
		rounded := (absVal + Scale/2) / Scale
		if neg && rounded > 0 {
			return fmt.Sprintf("-%d", rounded)
		}
		return fmt.Sprintf("%d", rounded)
	case decimals == 1:
		tenths := (absVal + 50) / 100
		if neg && tenths > 0 {
			return fmt.Sprintf("-%d.%d", tenths/10, tenths%10)
		}
		return fmt.Sprintf("%d.%d", tenths/10, tenths%10)
	case decimals == 2:
		hundredths := (absVal + 5) / 10
		if neg && hundredths > 0 {
			return fmt.Sprintf("-%d.%02d", hundredths/100, hundredths%100)
		}
		return fmt.Sprintf("%d.%02d", hundredths/100, hundredths%100)
	default:
		whole := absVal / Scale
		frac := absVal % Scale
		return fmt.Sprintf("%s%d.%03d", prefix, whole, frac)
	}
}

// String implements fmt.Stringer.
func (m Millis) String() string {
	return m.Format()
}
