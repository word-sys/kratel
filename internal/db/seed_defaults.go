package db

import (
	"context"
	"fmt"
	"time"
)

const (
	// DefaultLocationCode is the auto-created root bin code so users can scan items immediately on first launch.
	DefaultLocationCode = "MAIN"
	// DefaultLocationName is the display name for the default root storage location.
	DefaultLocationName = "Main Storage / Counter"
	// DefaultOperatorName is the initial operator profile created on first boot.
	DefaultOperatorName = "Admin"
)

type defaultSetting struct {
	key       string
	valueJSON string
}

var initialSettings = []defaultSetting{
	{key: "shop_name", valueJSON: `"Kratel Workshop"`},
	{key: "default_unit", valueJSON: `"pcs"`},
	{key: "currency", valueJSON: `"USD"`},
	{key: "allow_negative_stock", valueJSON: `false`},
	{key: "audio_enabled", valueJSON: `true`},
	{key: "locale", valueJSON: `"en"`},
	{key: "label_layout", valueJSON: `"thermal_50x25"`},
	{key: "expiry_warning_days", valueJSON: `30`},
}

// SeedDefaults idempotently populates the default MAIN storage location, initial shop preferences,
// and default Admin operator when initializing a database.
func SeedDefaults(ctx context.Context, exec sqlExecutor) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// 1. Ensure at least one location exists so immediate Quick-Scan IN/OUT works with zero setup.
	var locCount int
	if err := exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM locations;").Scan(&locCount); err != nil {
		return fmt.Errorf("count locations: %w", err)
	}
	if locCount == 0 {
		if _, err := exec.ExecContext(
			ctx,
			`INSERT INTO locations (code, name, parent_id, notes, created_at) VALUES (?, ?, NULL, ?, ?);`,
			DefaultLocationCode,
			DefaultLocationName,
			"Default primary storage location",
			now,
		); err != nil {
			return fmt.Errorf("insert default location: %w", err)
		}
	}

	// 2. Ensure default shop settings exist without overwriting user-customized keys.
	for _, s := range initialSettings {
		if _, err := exec.ExecContext(
			ctx,
			`INSERT OR IGNORE INTO settings (key, value_json, updated_at) VALUES (?, ?, ?);`,
			s.key,
			s.valueJSON,
			now,
		); err != nil {
			return fmt.Errorf("insert default setting %q: %w", s.key, err)
		}
	}

	// 3. Ensure at least one active operator exists for shared counter tagging.
	var opCount int
	if err := exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM operators;").Scan(&opCount); err != nil {
		return fmt.Errorf("count operators: %w", err)
	}
	if opCount == 0 {
		if _, err := exec.ExecContext(
			ctx,
			`INSERT INTO operators (name, role, pin_hash, active, created_at) VALUES (?, 'Manager', '', 1, ?);`,
			DefaultOperatorName,
			now,
		); err != nil {
			return fmt.Errorf("insert default operator: %w", err)
		}
	}

	return nil
}
