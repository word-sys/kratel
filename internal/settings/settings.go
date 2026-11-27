package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/db"
)

// ShopSettings holds user-configurable workshop preferences stored in the SQLite settings table.
type ShopSettings struct {
	ShopName           string `json:"shop_name"`
	DefaultUnit        string `json:"default_unit"`
	Currency           string `json:"currency"`
	AllowNegativeStock bool   `json:"allow_negative_stock"`
	AudioEnabled       bool   `json:"audio_enabled"`
	Locale             string `json:"locale"`
	LabelLayout        string `json:"label_layout"`
	ExpiryWarningDays  int    `json:"expiry_warning_days"`
}

// DefaultShopSettings returns Kratel's baseline shop configuration.
func DefaultShopSettings() ShopSettings {
	return ShopSettings{
		ShopName:           "Kratel Workshop",
		DefaultUnit:        "pcs",
		Currency:           "USD",
		AllowNegativeStock: false,
		AudioEnabled:       true,
		Locale:             "en",
		LabelLayout:        "thermal_50x25",
		ExpiryWarningDays:  30,
	}
}

var supportedLocales = map[string]bool{
	"en": true,
	"tr": true,
	"ar": true,
	"es": true,
	"de": true,
	"zh": true,
}

var supportedLabelLayouts = map[string]bool{
	"thermal_50x25": true,
	"thermal_40x30": true,
	"a4_3x8":        true,
	"letter_3x10":   true,
}

// Service manages persistent shop preferences and the lightweight operator directory.
type Service struct {
	store *db.Store
}

// NewService creates a settings & operators service backed by store.
func NewService(store *db.Store) *Service {
	return &Service{store: store}
}

// Get loads all shop settings from SQLite, falling back to defaults for any missing keys.
func (s *Service) Get(ctx context.Context) (*ShopSettings, error) {
	cfg := DefaultShopSettings()

	rows, err := s.store.DB().QueryContext(ctx, `SELECT key, value_json FROM settings;`)
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var key, valJSON string
		if err := rows.Scan(&key, &valJSON); err != nil {
			return nil, fmt.Errorf("scan setting row: %w", err)
		}
		switch key {
		case "shop_name":
			var v string
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil && strings.TrimSpace(v) != "" {
				cfg.ShopName = v
			}
		case "default_unit":
			var v string
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil && strings.TrimSpace(v) != "" {
				cfg.DefaultUnit = v
			}
		case "currency":
			var v string
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil {
				cfg.Currency = v
			}
		case "allow_negative_stock":
			var v bool
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil {
				cfg.AllowNegativeStock = v
			}
		case "audio_enabled":
			var v bool
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil {
				cfg.AudioEnabled = v
			}
		case "locale":
			var v string
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil && supportedLocales[v] {
				cfg.Locale = v
			}
		case "label_layout":
			var v string
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil && supportedLabelLayouts[v] {
				cfg.LabelLayout = v
			}
		case "expiry_warning_days":
			var v int
			if err := json.Unmarshal([]byte(valJSON), &v); err == nil && v > 0 {
				cfg.ExpiryWarningDays = v
			}
		}
	}

	return &cfg, nil
}

// Update validates and atomically persists shop settings into SQLite.
func (s *Service) Update(ctx context.Context, in ShopSettings) (*ShopSettings, error) {
	in.ShopName = strings.TrimSpace(in.ShopName)
	if in.ShopName == "" {
		in.ShopName = "Kratel Workshop"
	}

	in.DefaultUnit = strings.TrimSpace(in.DefaultUnit)
	if in.DefaultUnit == "" {
		in.DefaultUnit = "pcs"
	}

	in.Currency = strings.TrimSpace(in.Currency)
	if len(in.Currency) > 12 {
		return nil, apperr.Validation("currency", "currency symbol/code cannot exceed 12 characters")
	}

	in.Locale = strings.ToLower(strings.TrimSpace(in.Locale))
	if in.Locale == "" {
		in.Locale = "en"
	}
	if !supportedLocales[in.Locale] {
		return nil, apperr.Validation("locale", fmt.Sprintf("unsupported locale %q", in.Locale))
	}

	in.LabelLayout = strings.ToLower(strings.TrimSpace(in.LabelLayout))
	if in.LabelLayout == "" {
		in.LabelLayout = "thermal_50x25"
	}
	if !supportedLabelLayouts[in.LabelLayout] {
		return nil, apperr.Validation("label_layout", fmt.Sprintf("unsupported label layout %q", in.LabelLayout))
	}

	if in.ExpiryWarningDays <= 0 {
		in.ExpiryWarningDays = 30
	}
	if in.ExpiryWarningDays > 3650 {
		return nil, apperr.Validation("expiry_warning_days", "expiry_warning_days must be <= 3650")
	}

	pairs := []struct {
		key string
		val interface{}
	}{
		{"shop_name", in.ShopName},
		{"default_unit", in.DefaultUnit},
		{"currency", in.Currency},
		{"allow_negative_stock", in.AllowNegativeStock},
		{"audio_enabled", in.AudioEnabled},
		{"locale", in.Locale},
		{"label_layout", in.LabelLayout},
		{"expiry_warning_days", in.ExpiryWarningDays},
	}

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339)
		for _, p := range pairs {
			encoded, err := json.Marshal(p.val)
			if err != nil {
				return err
			}
			if _, execErr := tx.ExecContext(
				ctx,
				`INSERT INTO settings (key, value_json, updated_at)
				 VALUES (?, ?, ?)
				 ON CONFLICT(key) DO UPDATE SET
				     value_json = excluded.value_json,
				     updated_at = excluded.updated_at;`,
				p.key, string(encoded), now,
			); execErr != nil {
				return fmt.Errorf("upsert setting %q: %w", p.key, execErr)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.Get(ctx)
}
