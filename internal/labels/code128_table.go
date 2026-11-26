package labels

import (
	"fmt"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
)

const (
	code128CodeC  = 99
	code128CodeB  = 100
	code128StartB = 104
	code128StartC = 105
	code128Stop   = 106

	code128QuietModules = 10
)

// code128Patterns holds the official ISO/IEC 15417 Code128 bar/space width patterns
// for symbol values 0..106. Each value 0..105 has 6 alternating bar/space widths summing to 11 modules;
// value 106 (Stop) has 7 alternating bar/space widths summing to 13 modules.
var code128Patterns = [107]string{
	"212222", "222122", "222221", "121223", "121322", "131222", "122213", "122312", "132212", "221213", // 0-9
	"221312", "231212", "112232", "122132", "122231", "113222", "123122", "123221", "223211", "221132", // 10-19
	"221231", "213212", "223112", "312131", "311222", "321122", "321221", "312212", "322112", "322211", // 20-29
	"212123", "212321", "232121", "111323", "131123", "131321", "112313", "132113", "132311", "211313", // 30-39
	"231113", "231311", "112133", "112331", "132131", "113123", "113321", "133121", "313121", "211331", // 40-49
	"231131", "213113", "213311", "213131", "311123", "311321", "331121", "312113", "312311", "332111", // 50-59
	"314111", "221411", "431111", "111224", "111422", "121124", "121421", "141122", "141221", "112214", // 60-69
	"112412", "122114", "122411", "142112", "142211", "241211", "221114", "413111", "241112", "134111", // 70-79
	"111242", "121142", "121241", "114212", "124112", "124211", "411212", "421112", "421211", "212141", // 80-89
	"214121", "412121", "111143", "111341", "131141", "114113", "114311", "411113", "411311", "113141", // 90-99
	"114131", "311141", "411131", "211412", "211214", "211232", "2331112", // 100-106
}

// BarRun represents a single dark vertical bar in module coordinates (used by SVG and PDF renderers).
type BarRun struct {
	X     int
	Width int
}

// Code128Barcode holds the encoded symbol sequence, modulo-103 checksum, and vector bar runs.
type Code128Barcode struct {
	Text         string
	Symbols      []int
	Checksum     int
	TotalModules int // Includes leading & trailing 10-module quiet zones
	QuietModules int
	Bars         []BarRun
}

// EncodeCode128 encodes printable ASCII text (0x20..0x7E) into a scannable ISO/IEC 15417 Code128 barcode.
// It automatically switches between Code Set B (full ASCII) and Code Set C (double-density digit pairs)
// to keep long numeric barcodes compact on small 50x25mm and 40x30mm thermal labels.
func EncodeCode128(raw string) (*Code128Barcode, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, apperr.Validation("code", "barcode value cannot be empty")
	}
	for i := 0; i < len(text); i++ {
		if text[i] < 32 || text[i] > 126 {
			return nil, apperr.Validation("code", fmt.Sprintf("Code128 supports printable ASCII only (invalid byte 0x%02X at pos %d)", text[i], i))
		}
	}

	symbols := make([]int, 0, len(text)+4)
	pos := 0
	n := len(text)

	// Decide initial code set: Start C if text starts with 4+ digits or is entirely 2+ even digits
	inCodeC := false
	leadingDigits := countLeadingDigits(text, 0)
	if leadingDigits >= 4 || (leadingDigits == n && n >= 2 && n%2 == 0) {
		symbols = append(symbols, code128StartC)
		inCodeC = true
	} else {
		symbols = append(symbols, code128StartB)
		inCodeC = false
	}

	for pos < n {
		if inCodeC {
			if pos+2 <= n && isDigit(text[pos]) && isDigit(text[pos+1]) {
				pair := int(text[pos]-'0')*10 + int(text[pos+1]-'0')
				symbols = append(symbols, pair)
				pos += 2
				continue
			}
			// Switch back to Code Set B
			symbols = append(symbols, code128CodeB)
			inCodeC = false
		}

		// In Code Set B: switch to Code Set C if 4+ consecutive digits remain
		remDigits := countLeadingDigits(text, pos)
		if remDigits >= 4 {
			if remDigits%2 == 1 {
				symbols = append(symbols, int(text[pos])-32)
				pos++
			}
			symbols = append(symbols, code128CodeC)
			inCodeC = true
			continue
		}

		symbols = append(symbols, int(text[pos])-32)
		pos++
	}

	// Calculate Modulo-103 weighted check symbol
	sum := symbols[0]
	for i := 1; i < len(symbols); i++ {
		sum += i * symbols[i]
	}
	checksum := sum % 103
	symbols = append(symbols, checksum, code128Stop)

	// Expand symbol widths into dark vertical BarRuns offset by the leading quiet zone
	x := code128QuietModules
	bars := make([]BarRun, 0, len(symbols)*3+1)
	for _, sym := range symbols {
		pat := code128Patterns[sym]
		for i := 0; i < len(pat); i++ {
			w := int(pat[i] - '0')
			if i%2 == 0 {
				bars = append(bars, BarRun{X: x, Width: w})
			}
			x += w
		}
	}
	totalModules := x + code128QuietModules

	return &Code128Barcode{
		Text:         text,
		Symbols:      symbols,
		Checksum:     checksum,
		TotalModules: totalModules,
		QuietModules: code128QuietModules,
		Bars:         bars,
	}, nil
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func countLeadingDigits(s string, start int) int {
	c := 0
	for i := start; i < len(s) && isDigit(s[i]); i++ {
		c++
	}
	return c
}
