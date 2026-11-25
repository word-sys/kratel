package csvmig

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// StripUTF8BOM removes a leading UTF-8 Byte Order Mark (\xEF\xBB\xBF) if present.
func StripUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, utf8BOM)
}

// DetectDelimiter inspects the first non-empty lines of a CSV/TSV payload and returns
// the most likely field delimiter among comma (','), semicolon (';'), and tab ('\t').
func DetectDelimiter(data []byte) rune {
	cleaned := StripUTF8BOM(data)
	sample := string(cleaned)
	if len(sample) > 8192 {
		sample = sample[:8192]
	}

	lines := splitSampleLines(sample, 10)
	if len(lines) == 0 {
		return ','
	}

	candidates := []rune{'\t', ';', ','}
	bestDelim := ','
	bestScore := -1

	for _, delim := range candidates {
		counts := make([]int, 0, len(lines))
		total := 0
		for _, ln := range lines {
			c := countUnquotedRune(ln, delim)
			counts = append(counts, c)
			total += c
		}
		if total == 0 {
			continue
		}

		// Favor delimiters that appear consistently across every sample row (especially header + data rows)
		first := counts[0]
		if first == 0 {
			continue
		}
		consistent := 0
		for _, c := range counts {
			if c == first {
				consistent++
			}
		}
		score := consistent*1000 + first*10
		if delim == '\t' {
			// Tabs inside unquoted spreadsheet text almost always mean Excel/Sheets clipboard TSV
			score += 50
		}
		if score > bestScore {
			bestScore = score
			bestDelim = delim
		}
	}

	return bestDelim
}

// ParseTable strips any UTF-8 BOM, detects or uses forcedDelimiter, and parses the input
// into cleaned headers and rectangular data rows (skipping completely blank lines).
func ParseTable(raw []byte, forcedDelimiter rune) ([]string, [][]string, rune, error) {
	cleaned := StripUTF8BOM(raw)
	if len(bytes.TrimSpace(cleaned)) == 0 {
		return nil, nil, ',', apperr.Validation("csv", "spreadsheet content is empty")
	}

	delim := forcedDelimiter
	if delim == 0 {
		delim = DetectDelimiter(cleaned)
	}

	reader := csv.NewReader(bytes.NewReader(cleaned))
	reader.Comma = delim
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = false

	var allRows [][]string
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, delim, apperr.Validation("csv", fmt.Sprintf("failed to parse CSV/TSV: %v", err))
		}
		if isRowEmpty(rec) {
			continue
		}
		allRows = append(allRows, rec)
	}

	if len(allRows) == 0 {
		return nil, nil, delim, apperr.Validation("csv", "spreadsheet contains no header row")
	}

	rawHeaders := allRows[0]
	headers := make([]string, len(rawHeaders))
	seen := make(map[string]int, len(rawHeaders))
	for i, h := range rawHeaders {
		name := strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		if name == "" {
			name = fmt.Sprintf("Column_%d", i+1)
		}
		key := strings.ToLower(name)
		if count := seen[key]; count > 0 {
			seen[key] = count + 1
			name = fmt.Sprintf("%s_%d", name, count+1)
		} else {
			seen[key] = 1
		}
		headers[i] = name
	}

	dataRows := make([][]string, 0, len(allRows)-1)
	colCount := len(headers)
	for _, r := range allRows[1:] {
		norm := make([]string, colCount)
		for i := 0; i < colCount && i < len(r); i++ {
			norm[i] = strings.TrimSpace(r[i])
		}
		if !isRowEmpty(norm) {
			dataRows = append(dataRows, norm)
		}
	}

	return headers, dataRows, delim, nil
}

func splitSampleLines(s string, maxLines int) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	raw := strings.Split(s, "\n")
	out := make([]string, 0, maxLines)
	for _, ln := range raw {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		out = append(out, ln)
		if len(out) >= maxLines {
			break
		}
	}
	return out
}

func countUnquotedRune(line string, target rune) int {
	inQuotes := false
	count := 0
	for _, r := range line {
		if r == '"' {
			inQuotes = !inQuotes
			continue
		}
		if !inQuotes && r == target {
			count++
		}
	}
	return count
}

func isRowEmpty(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}
