package num

import "testing"

func TestLineValuationAndPortfolioSum(t *testing.T) {
	// 2500 pcs of 0805 SMD resistor at 0.005 unit cost -> 2500.000 * 0.005 = 12.500 (12500 Millis)
	qty := FromUnits(2500)
	cost := Millis(5) // 0.005
	val, err := LineValuation(qty, cost)
	if err != nil {
		t.Fatalf("LineValuation failed: %v", err)
	}
	if val != 12500 {
		t.Fatalf("expected valuation 12500 Millis (12.50), got %d", val)
	}

	// 1.750 meters of silicone cable at 4.20 unit cost -> 1.75 * 4.20 = 7.350 (7350 Millis)
	cableQty := Millis(1750)
	cableCost := Millis(4200)
	cableVal, err := LineValuation(cableQty, cableCost)
	if err != nil {
		t.Fatalf("LineValuation failed: %v", err)
	}
	if cableVal != 7350 {
		t.Fatalf("expected valuation 7350 Millis (7.35), got %d", cableVal)
	}

	total, err := SumValuations(val, cableVal)
	if err != nil {
		t.Fatalf("SumValuations failed: %v", err)
	}
	if total != 19850 {
		t.Fatalf("expected total 19850 Millis (19.85), got %d", total)
	}

	// Negative stock balance contributes 0 in LineValuationSafe
	if safe := LineValuationSafe(Millis(-5000), Millis(10000)); safe != 0 {
		t.Errorf("expected LineValuationSafe on negative stock to return 0, got %d", safe)
	}
}

func TestFormatMoneyRegionAgnostic(t *testing.T) {
	cases := []struct {
		amount   Millis
		currency string
		want     string
	}{
		{amount: 12500, currency: "", want: "12.50"},
		{amount: 12500, currency: "$", want: "$12.50"},
		{amount: 12500, currency: "₺", want: "₺12.50"},
		{amount: 12500, currency: "TRY", want: "12.50 TRY"},
		{amount: 12500, currency: "EUR", want: "12.50 EUR"},
		{amount: 5, currency: "$", want: "$0.005"},
		{amount: 5, currency: "USD", want: "0.005 USD"},
		{amount: -4250, currency: "€", want: "-€4.25"},
		{amount: -4250, currency: "TRY", want: "-4.25 TRY"},
		{amount: 0, currency: "₺", want: "₺0.00"},
	}

	for _, tc := range cases {
		if got := FormatMoney(tc.amount, tc.currency); got != tc.want {
			t.Errorf("FormatMoney(%d, %q) = %q, want %q", tc.amount, tc.currency, got, tc.want)
		}
	}
}
