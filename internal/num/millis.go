package num

import (
	"fmt"
	"math"
	"math/big"

	"github.com/word-sys/kratel/internal/apperr"
)

const (
	// Scale is the fixed-point multiplier: 1.000 unit == 1000 Millis.
	Scale int64 = 1000

	// MaxSafeMillis caps quantities at 9,000,000,000,000.000 units (9e15 millis),
	// staying safely inside both int64 and IEEE-754 double-precision exact integer bounds (2^53 - 1).
	MaxSafeMillis int64 = 9_000_000_000_000_000
	MinSafeMillis int64 = -MaxSafeMillis
)

// Millis represents an exact fixed-point quantity or unit cost scaled by 1000 (thousandths of a unit).
// Using integer Millis eliminates IEEE-754 floating-point drift (e.g. 0.1 + 0.2 == 0.300).
type Millis int64

// FromUnits converts a whole integer unit count into Millis (e.g. 5 -> 5000).
func FromUnits(whole int64) Millis {
	return Millis(whole * Scale)
}

// FromFloat converts a float64 to the nearest fixed-point Millis with NaN/Inf/bounds validation.
func FromFloat(f float64) (Millis, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, apperr.Validation("quantity", "numeric value cannot be NaN or Infinity")
	}
	scaled := math.Round(f * float64(Scale))
	if scaled > float64(MaxSafeMillis) || scaled < float64(MinSafeMillis) {
		return 0, apperr.Validation("quantity", fmt.Sprintf("numeric value %g exceeds maximum supported magnitude", f))
	}
	return Millis(int64(scaled)), nil
}

// MustFromFloat converts a float64 to the nearest Millis, returning 0 if invalid.
func MustFromFloat(f float64) Millis {
	m, err := FromFloat(f)
	if err != nil {
		return 0
	}
	return m
}

// ToFloat converts Millis to a standard float64 for SQLite REAL column synchronization and JSON display.
func (m Millis) ToFloat() float64 {
	return float64(m) / float64(Scale)
}

// Int64 returns the raw scaled int64 thousandths value.
func (m Millis) Int64() int64 {
	return int64(m)
}

// IsZero reports whether m == 0.
func (m Millis) IsZero() bool {
	return m == 0
}

// IsPositive reports whether m > 0.
func (m Millis) IsPositive() bool {
	return m > 0
}

// IsNegative reports whether m < 0.
func (m Millis) IsNegative() bool {
	return m < 0
}

// IsWhole reports whether m has zero fractional thousandths (e.g. 2000 -> true, 2500 -> false).
func (m Millis) IsWhole() bool {
	return int64(m)%Scale == 0
}

// WholeUnits returns the truncated integer unit portion of m.
func (m Millis) WholeUnits() int64 {
	return int64(m) / Scale
}

// FractionalMillis returns the non-negative fractional thousandths portion in [0, 999].
func (m Millis) FractionalMillis() int64 {
	rem := int64(m) % Scale
	if rem < 0 {
		rem = -rem
	}
	return rem
}

// Neg returns the additive inverse (-m).
func (m Millis) Neg() Millis {
	return -m
}

// Abs returns the absolute value of m.
func (m Millis) Abs() Millis {
	if m < 0 {
		return -m
	}
	return m
}

// Add returns m + other.
func (m Millis) Add(other Millis) Millis {
	return m + other
}

// Sub returns m - other.
func (m Millis) Sub(other Millis) Millis {
	return m - other
}

// CheckedAdd returns m + other, or a validation error if the result exceeds safe bounds.
func (m Millis) CheckedAdd(other Millis) (Millis, error) {
	a := int64(m)
	b := int64(other)
	if (b > 0 && a > MaxSafeMillis-b) || (b < 0 && a < MinSafeMillis-b) {
		return 0, apperr.Validation("quantity", "quantity addition exceeds safe fixed-point bounds")
	}
	sum := a + b
	if sum > MaxSafeMillis || sum < MinSafeMillis {
		return 0, apperr.Validation("quantity", "quantity addition exceeds safe fixed-point bounds")
	}
	return Millis(sum), nil
}

// CheckedSub returns m - other, or a validation error if the result exceeds safe bounds.
func (m Millis) CheckedSub(other Millis) (Millis, error) {
	return m.CheckedAdd(-other)
}

// MulMillis multiplies two fixed-point Millis values (e.g. quantity_millis * unit_cost_millis / 1000)
// using big.Int intermediate arithmetic to prevent int64 overflow and rounds half-away-from-zero.
func (m Millis) MulMillis(other Millis) (Millis, error) {
	if m == 0 || other == 0 {
		return 0, nil
	}

	prod := new(big.Int).Mul(big.NewInt(int64(m)), big.NewInt(int64(other)))
	sign := prod.Sign()
	prod.Abs(prod)

	// Half-up rounding: (|a * b| + 500) / 1000
	prod.Add(prod, big.NewInt(Scale/2))
	prod.Quo(prod, big.NewInt(Scale))

	if sign < 0 {
		prod.Neg(prod)
	}

	if !prod.IsInt64() {
		return 0, apperr.Validation("quantity", "fixed-point multiplication overflow")
	}
	res := prod.Int64()
	if res > MaxSafeMillis || res < MinSafeMillis {
		return 0, apperr.Validation("quantity", "fixed-point multiplication exceeds safe bounds")
	}
	return Millis(res), nil
}
