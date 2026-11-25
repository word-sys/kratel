package csvmig

// CSVPreview holds the analyzed header list, sample rows, detected delimiter,
// and intelligent auto-mapped column assignments for the interactive CSV Migration Wizard.
type CSVPreview struct {
	DetectedDelimiter string              `json:"detected_delimiter"`
	TotalRows         int                 `json:"total_rows"`
	Headers           []string            `json:"headers"`
	SampleRows        [][]string          `json:"sample_rows"`
	SuggestedMappings []ColumnMapping     `json:"suggested_mappings"`
	SupportedTargets  []TargetFieldOption `json:"supported_targets"`
}

// AnalyzeCSV parses raw CSV/TSV bytes (from file upload or Excel/Google Sheets clipboard paste),
// detects delimiter and BOM, extracts up to 10 sample rows, and proposes automatic column mappings.
func AnalyzeCSV(data []byte, forcedDelim rune) (*CSVPreview, error) {
	headers, rows, delim, err := ParseCSVBytes(data, forcedDelim)
	if err != nil {
		return nil, err
	}

	sampleLimit := 10
	if len(rows) < sampleLimit {
		sampleLimit = len(rows)
	}
	samples := make([][]string, sampleLimit)
	for i := 0; i < sampleLimit; i++ {
		normalized := make([]string, len(headers))
		for col := 0; col < len(headers) && col < len(rows[i]); col++ {
			normalized[col] = rows[i][col]
		}
		samples[i] = normalized
	}

	return &CSVPreview{
		DetectedDelimiter: DelimiterLabel(delim),
		TotalRows:         len(rows),
		Headers:           headers,
		SampleRows:        samples,
		SuggestedMappings: AutoMapHeaders(headers),
		SupportedTargets:  SupportedTargetFields,
	}, nil
}
