package num

import (
	"math"
	"testing"

	"github.com/word-sys/kratel/internal/apperr"
)

func TestZeroFloatingPointDrift(t *testing.T) {
	// Classic IEEE-754 drift test: 0.1 + 0.2 must equal 0.300 (300 Millis) exactly.
	a, err := ParseMillis("0.1")
	if err != nil {
		t.Fatalf("ParseMillis(0.1) failed: %v", err)
	}
	b, err := ParseMillis("0.2")
	if err != nil {
		t.Fatalf("ParseMillis(0.2) failed: %v", err)
	}
	sum, err := a.CheckedAdd(b)
	if err != nil {
		t.Fatalf("CheckedAdd failed: %v", err)
	}
	if sum != 300 {
		t.Fatalf("expected 300 Millis, got %d", sum)
	}
	if sum.Format() != "0.3" {
		t.Fatalf("expected formatted '0.3', got %q", sum.Format())
	}
	if sum.FormatFixed(3) != "0.300" {
		t.Fatalf("expected FormatFixed(3) '0.300', got %q", sum.FormatFixed(3))
	}
}

func TestParseMillisTable(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantMillis Millis
		wantFormat string
		wantErr    bool
	}{
		{name: "integer", input: "42", wantMillis: 42000, wantFormat: "42"},
		{name: "zero", input: "0", wantMillis: 0, wantFormat: "0"},
		{name: "negativeZero", input: "-0.000", wantMillis: 0, wantFormat: "0"},
		{name: "dotOneDecimal", input: "1.5", wantMillis: 1500, wantFormat: "1.5"},
		{name: "dotTwoDecimals", input: "12.34", wantMillis: 12340, wantFormat: "12.34"},
		{name: "dotThreeDecimals", input: "0.125", wantMillis: 125, wantFormat: "0.125"},
		{name: "commaDecimalTurkish", input: "12,75", wantMillis: 12750, wantFormat: "12.75"},
		{name: "leadingDot", input: ".5", wantMillis: 500, wantFormat: "0.5"},
		{name: "leadingComma", input: ",25", wantMillis: 250, wantFormat: "0.25"},
		{name: "trailingDot", input: "9.", wantMillis: 9000, wantFormat: "9"},
		{name: "positiveSign", input: "+3.125", wantMillis: 3125, wantFormat: "3.125"},
		{name: "negativeFraction", input: "-0.05", wantMillis: -50, wantFormat: "-0.05"},
		{name: "usThousandsAndDecimal", input: "1,250.75", wantMillis: 1250750, wantFormat: "1250.75"},
		{name: "euTurkishThousandsAndDecimal", input: "1.250,75", wantMillis: 1250750, wantFormat: "1250.75"},
		{name: "multipleCommasThousands", input: "1,000,000", wantMillis: 1000000000, wantFormat: "1000000"},
		{name: "multipleDotsThousands", input: "1.000.000", wantMillis: 1000000000, wantFormat: "1000000"},
		{name: "roundFourDecimalsUp", input: "1.2345", wantMillis: 1235, wantFormat: "1.235"},
		{name: "roundFourDecimalsDown", input: "1.2344", wantMillis: 1234, wantFormat: "1.234"},
		{name: "whitespaceAndNBSP", input: "  25.5\u00a0 ", wantMillis: 25500, wantFormat: "25.5"},
		{name: "emptyString", input: "   ", wantErr: true},
		{name: "onlySign", input: "-", wantErr: true},
		{name: "onlyDot", input: ".", wantErr: true},
		{name: "alphabetic", input: "12pcs", wantErr: true},
		{name: "invalidMultipleDots", input: "12.34.56", wantErr: true},
		{name: "invalidMixedOrder", input: "1,23.4,5", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMillis(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got %d", tc.input, got)
				}
				if !apperr.HasCode(err, apperr.CodeValidation) {
					t.Fatalf("expected VALIDATION_ERROR code, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got != tc.wantMillis {
				t.Errorf("ParseMillis(%q) = %d, want %d", tc.input, got, tc.wantMillis)
			}
			if got.Format() != tc.wantFormat {
				t.Errorf("(%d).Format() = %q, want %q", got, got.Format(), tc.wantFormat)
			}
		})
	}
}

func TestPositiveAndNonNegativeValidators(t *testing.T) {
	if _, err := ParsePositiveMillis("quantity", "0"); err == nil {
		t.Error("expected ParsePositiveMillis('0') to fail")
	}
	if _, err := ParsePositiveMillis("quantity", "-1"); err == nil {
		t.Error("expected ParsePositiveMillis('-1') to fail")
	}
	pos, err := ParsePositiveMillis("quantity", "0.001")
	if err != nil || pos != 1 {
		t.Errorf("expected 1 Millis, got %d (err=%v)", pos, err)
	}

	z, err := ParseNonNegativeMillis("min_stock", "")
	if err != nil || z != 0 {
		t.Errorf("expected empty string to parse as 0 for NonNegative, got %d (err=%v)", z, err)
	}
	if _, err := ParseNonNegativeMillis("min_stock", "-0.001"); err == nil {
		t.Error("expected ParseNonNegativeMillis('-0.001') to fail")
	}
}

func TestMillisPropertiesAndBounds(t *testing.T) {
	m := FromUnits(7)
	if m != 7000 || !m.IsWhole() || !m.IsPositive() || m.IsZero() || m.IsNegative() {
		t.Fatalf("unexpected properties for FromUnits(7): %d", m)
	}
	if m.WholeUnits() != 7 || m.FractionalMillis() != 0 {
		t.Fatalf("unexpected WholeUnits/FractionalMillis for %d", m)
	}

	negFrac := Millis(-2375)
	if negFrac.WholeUnits() != -2 || negFrac.FractionalMillis() != 375 || negFrac.IsWhole() {
		t.Fatalf("unexpected breakdown for -2375: whole=%d frac=%d", negFrac.WholeUnits(), negFrac.FractionalMillis())
	}
	if negFrac.Abs() != 2375 || negFrac.Neg() != 2375 {
		t.Fatalf("unexpected Abs/Neg for -2375")
	}

	if _, err := FromFloat(math.NaN()); err == nil {
		t.Error("expected NaN to be rejected")
	}
	if _, err := FromFloat(math.Inf(1)); err == nil {
		t.Error("expected +Inf to be rejected")
	}

	maxM := Millis(MaxSafeMillis)
	if _, err := maxM.CheckedAdd(1); err == nil {
		t.Error("expected CheckedAdd past MaxSafeMillis to return overflow error")
	}
	minM := Millis(MinSafeMillis)
	if _, err := minM.CheckedSub(1); err == nil {
		t.Error("expected CheckedSub past MinSafeMillis to return underflow error")
	}
}

func TestFormatFixedVariants(t *testing.T) {
	m := Millis(12346) // 12.346
	if got := m.FormatFixed(0); got != "12" {
		t.Errorf("FormatFixed(0) = %q, want '12'", got)
	}
	if got := m.FormatFixed(1); got != "12.3" {
		t.Errorf("FormatFixed(1) = %q, want '12.3'", got)
	}
	if got := m.FormatFixed(2); got != "12.35" {
		t.Errorf("FormatFixed(2) = %q, want '12.35'", got)
	}
	if got := m.FormatFixed(3); got != "12.346" {
		t.Errorf("FormatFixed(3) = %q, want '12.346'", got)
	}

	negSmall := Millis(-4) // -0.004 rounds to 0 at 2 decimals, must not output "-0.00"
	if got := negSmall.FormatFixed(2); got != "0.00" {
		t.Errorf("(-4).FormatFixed(2) = %q, want '0.00'", got)
	}
}
