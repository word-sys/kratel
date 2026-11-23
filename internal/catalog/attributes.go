package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
)

const (
	MaxCustomAttributes  = 64
	MaxAttributeKeyLen   = 64
	MaxAttributeValueLen = 512
)

// AttributeFacet summarizes a discovered custom JSON attribute key and its distinct sample values.
type AttributeFacet struct {
	Key       string   `json:"key"`
	ItemCount int      `json:"item_count"`
	Values    []string `json:"values"`
}

// NormalizeAttributes merges and validates either a raw JSON object string or a key-value map
// into a deterministic, canonical JSON string ("{}" if empty) and a clean map[string]string.
func NormalizeAttributes(rawJSON string, mapInput map[string]string) (string, map[string]string, error) {
	merged := make(map[string]string)

	trimmed := strings.TrimSpace(rawJSON)
	if trimmed != "" && trimmed != "{}" && trimmed != "null" {
		var rawMap map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &rawMap); err != nil {
			return "", nil, apperr.Validation("attributes_json", "attributes_json must be a valid JSON object")
		}
		for k, v := range rawMap {
			if v == nil {
				continue
			}
			merged[k] = fmt.Sprintf("%v", v)
		}
	}

	for k, v := range mapInput {
		merged[k] = v
	}

	cleaned := make(map[string]string, len(merged))
	for k, v := range merged {
		ck := strings.TrimSpace(k)
		cv := strings.TrimSpace(v)
		if ck == "" || cv == "" {
			continue
		}
		if len(ck) > MaxAttributeKeyLen {
			return "", nil, apperr.Validation("attributes_json", fmt.Sprintf("attribute key %q exceeds %d characters", ck, MaxAttributeKeyLen))
		}
		if len(cv) > MaxAttributeValueLen {
			return "", nil, apperr.Validation("attributes_json", fmt.Sprintf("attribute value for %q exceeds %d characters", ck, MaxAttributeValueLen))
		}
		cleaned[ck] = cv
	}

	if len(cleaned) > MaxCustomAttributes {
		return "", nil, apperr.Validation("attributes_json", fmt.Sprintf("cannot exceed %d custom attributes per item", MaxCustomAttributes))
	}
	if len(cleaned) == 0 {
		return "{}", map[string]string{}, nil
	}

	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return "", nil, apperr.Validation("attributes_json", "failed to serialize attributes_json")
	}
	return string(encoded), cleaned, nil
}

// ParseAttributesJSON safely parses a stored attributes_json string into a non-nil map[string]string.
func ParseAttributesJSON(rawJSON string) map[string]string {
	out := make(map[string]string)
	trimmed := strings.TrimSpace(rawJSON)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return out
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &rawMap); err != nil {
		return out
	}
	for k, v := range rawMap {
		if v == nil {
			continue
		}
		ck := strings.TrimSpace(k)
		cv := strings.TrimSpace(fmt.Sprintf("%v", v))
		if ck != "" && cv != "" {
			out[ck] = cv
		}
	}
	return out
}

// MatchesAttributeFilter checks whether an item's custom attributes match the requested key and optional value substring.
func MatchesAttributeFilter(attrs map[string]string, filterKey, filterVal string) bool {
	fk := strings.ToLower(strings.TrimSpace(filterKey))
	fv := strings.ToLower(strings.TrimSpace(filterVal))
	if fk == "" && fv == "" {
		return true
	}

	for k, v := range attrs {
		kl := strings.ToLower(k)
		vl := strings.ToLower(v)
		if fk != "" && kl != fk {
			continue
		}
		if fv == "" || strings.Contains(vl, fv) {
			return true
		}
	}
	return false
}

// DiscoverAttributeKeys scans active catalog items to return all distinct custom attribute keys and their top values.
func DiscoverAttributeKeys(ctx context.Context, conn *sql.DB) ([]AttributeFacet, error) {
	rows, err := conn.QueryContext(
		ctx,
		`SELECT attributes_json FROM items WHERE archived = 0 AND attributes_json != '{}' AND attributes_json != '';`,
	)
	if err != nil {
		return nil, fmt.Errorf("query custom attribute keys: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	keyCounts := make(map[string]int)
	keyValues := make(map[string]map[string]struct{})

	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan attributes_json: %w", err)
		}
		attrs := ParseAttributesJSON(raw)
		for k, v := range attrs {
			keyCounts[k]++
			valSet, ok := keyValues[k]
			if !ok {
				valSet = make(map[string]struct{})
				keyValues[k] = valSet
			}
			if len(valSet) < 25 {
				valSet[v] = struct{}{}
			}
		}
	}

	facets := make([]AttributeFacet, 0, len(keyCounts))
	for k, count := range keyCounts {
		vals := make([]string, 0, len(keyValues[k]))
		for v := range keyValues[k] {
			vals = append(vals, v)
		}
		sort.Strings(vals)
		facets = append(facets, AttributeFacet{
			Key:       k,
			ItemCount: count,
			Values:    vals,
		})
	}

	sort.Slice(facets, func(i, j int) bool {
		if facets[i].ItemCount != facets[j].ItemCount {
			return facets[i].ItemCount > facets[j].ItemCount
		}
		return facets[i].Key < facets[j].Key
	})

	return facets, nil
}
