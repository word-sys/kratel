package csvmig

// CSVPreview summarizes a parsed CSV or Excel-pasted TSV payload for the interactive column mapper.
type CSVPreview struct {
	DetectedDelimiter string            `json:"detected_delimiter"`
	Headers           []string          `json:"headers"`
	SampleRows        [][]string        `json:"sample_rows"`
	TotalRows         int               `json:"total_rows"`
	SuggestedMapping  map[string]string `json:"suggested_mapping"`
	AvailableFields   []FieldOption     `json:"available_fields"`
}

// AnalyzeCSV parses raw CSV or pasted TSV bytes, returning detected headers, up to 10 sample rows,
// total data row count, detected delimiter, and intelligent column mapping suggestions.
func AnalyzeCSV(raw []byte, forcedDelimiter rune) (*CSVPreview, error) {
	headers, rows, delim, err := ParseTable(raw, forcedDelimiter)
	if err != nil {
		return nil, err
	}

	sampleLimit := 10
	if len(rows) < sampleLimit {
		sampleLimit = len(rows)
	}
	samples := make([][]string, sampleLimit)
	copy(samples, rows[:sampleLimit])

	return &CSVPreview{
		DetectedDelimiter: string(delim),
		Headers:           headers,
		SampleRows:        samples,
		TotalRows:         len(rows),
		SuggestedMapping:  SuggestColumnMapping(headers),
		AvailableFields:   AvailableTargetFields(),
	}, nil
}
