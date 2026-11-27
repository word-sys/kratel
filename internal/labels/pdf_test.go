package labels

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
)

func verifyValidPDFStructure(t *testing.T, pdf []byte, expectedPages int) {
	t.Helper()

	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) {
		t.Fatalf("PDF missing %%PDF-1.4 header")
	}
	if !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatalf("PDF missing %%%%EOF trailer")
	}

	s := string(pdf)
	wantCount := fmt.Sprintf("/Count %d", expectedPages)
	if !strings.Contains(s, wantCount) {
		t.Fatalf("PDF missing expected %q", wantCount)
	}

	// Verify startxref points to exact byte offset of the "xref\n" table
	reStartXref := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`)
	m := reStartXref.FindStringSubmatch(s)
	if len(m) != 2 {
		t.Fatalf("failed to locate startxref offset in PDF trailer")
	}
	xrefOff, err := strconv.Atoi(m[1])
	if err != nil || xrefOff <= 0 || xrefOff >= len(pdf) {
		t.Fatalf("invalid startxref offset %q", m[1])
	}
	if !bytes.HasPrefix(pdf[xrefOff:], []byte("xref\n")) {
		t.Fatalf("startxref byte offset %d does not point to 'xref\\n' (got %q)", xrefOff, string(pdf[xrefOff:xrefOff+10]))
	}

	// Verify every object entry in the xref table points to the exact "<id> 0 obj" header
	expectedObjs := 7 + expectedPages*2
	for id := 1; id <= expectedObjs; id++ {
		lines := strings.Split(s[xrefOff:], "\n")
		// lines[0]="xref", lines[1]="0 N", lines[2]="0000000000 65535 f ", lines[2+id]=obj entry
		if 2+id >= len(lines) {
			t.Fatalf("xref missing entry for object %d", id)
		}
		fields := strings.Fields(lines[2+id])
		if len(fields) != 3 || fields[2] != "n" {
			t.Fatalf("malformed xref line for object %d: %q", id, lines[2+id])
		}
		objOff, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Fatalf("invalid object offset %q: %v", fields[0], err)
		}
		wantPrefix := fmt.Sprintf("%d 0 obj\n", id)
		if !bytes.HasPrefix(pdf[objOff:], []byte(wantPrefix)) {
			t.Errorf("xref offset %d for object %d does not match %q", objOff, id, wantPrefix)
		}
	}
}

func TestPDFAllLayoutsPageCountsAndSymbologies(t *testing.T) {
	makeLabels := func(n int) []LabelItem {
		out := make([]LabelItem, n)
		for i := 0; i < n; i++ {
			out[i] = LabelItem{
				Title:    fmt.Sprintf("10kΩ Resistor 0805 #%d", i+1),
				Subtitle: fmt.Sprintf("SKU: RES-%04d | Bin: A-01", i+1),
				Code:     fmt.Sprintf("KRT-%06d", i+1),
				Badge:    "pcs",
			}
		}
		return out
	}

	cases := []struct {
		name          string
		layout        LayoutID
		style         LabelStyle
		labelCount    int
		expectedPages int
	}{
		{name: "Thermal50x25_Code128_3Labels", layout: LayoutThermal50x25, style: StyleCode128, labelCount: 3, expectedPages: 3},
		{name: "Thermal40x30_QR_2Labels", layout: LayoutThermal40x30, style: StyleQR, labelCount: 2, expectedPages: 2},
		{name: "A4_3x8_Code128_24Labels_1Page", layout: LayoutA4_3x8, style: StyleCode128, labelCount: 24, expectedPages: 1},
		{name: "A4_3x8_QR_25Labels_2Pages", layout: LayoutA4_3x8, style: StyleQR, labelCount: 25, expectedPages: 2},
		{name: "Letter_3x10_Code128_30Labels_1Page", layout: LayoutLetter3x10, style: StyleCode128, labelCount: 30, expectedPages: 1},
		{name: "Letter_3x10_QR_61Labels_3Pages", layout: LayoutLetter3x10, style: StyleQR, labelCount: 61, expectedPages: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pdfBytes, err := RenderLabelsPDF(makeLabels(tc.labelCount), PDFRenderOptions{
				Layout: tc.layout,
				Style:  tc.style,
			})
			if err != nil {
				t.Fatalf("RenderLabelsPDF failed: %v", err)
			}
			verifyValidPDFStructure(t, pdfBytes, tc.expectedPages)
		})
	}
}

func TestLabelServiceCatalogLocationsAndTurkishTransliteration(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:) failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	cat := catalog.NewService(store)
	lblSvc := NewService(store, cat)

	bin, err := cat.CreateLocation(ctx, catalog.LocationInput{
		Code: "RACK-A1",
		Name: "Geliştirme Rafı A1",
	})
	if err != nil {
		t.Fatalf("CreateLocation failed: %v", err)
	}

	item, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:               "LED-RGB-WS2812",
		Barcode:           "8690005554443",
		Name:              "WS2812B Şerit LED Modülü (10Ω)",
		Category:          "Optoelectronics",
		Unit:              "pcs",
		DefaultLocationID: &bin.ID,
	})
	if err != nil {
		t.Fatalf("CreateItem failed: %v", err)
	}

	// Verify single SVG generation for item & location
	itemSVG, itemLbl, err := lblSvc.RenderSingleSVG(ctx, LabelEntryRequest{
		TargetType: TargetItem,
		ID:         item.ID,
	}, StyleCode128)
	if err != nil {
		t.Fatalf("RenderSingleSVG(item) failed: %v", err)
	}
	if !strings.Contains(itemSVG, "<svg ") || itemLbl.Code != "8690005554443" {
		t.Fatalf("unexpected item SVG/label: code=%q", itemLbl.Code)
	}

	locSVG, locLbl, err := lblSvc.RenderSingleSVG(ctx, LabelEntryRequest{
		TargetType: TargetLocation,
		ID:         bin.ID,
	}, StyleQR)
	if err != nil {
		t.Fatalf("RenderSingleSVG(location) failed: %v", err)
	}
	if !strings.Contains(locSVG, "<svg ") || locLbl.Code != "LOC:RACK-A1" {
		t.Fatalf("unexpected location SVG/label: code=%q", locLbl.Code)
	}

	// Generate batch PDF combining item copies + location label + custom label
	pdfBytes, err := lblSvc.GenerateBatchPDF(ctx, PDFBatchRequest{
		Layout: LayoutA4_3x8,
		Style:  StyleCode128,
		Entries: []LabelEntryRequest{
			{TargetType: TargetItem, ID: item.ID, Copies: 4},
			{TargetType: TargetLocation, ID: bin.ID, Copies: 2},
			{TargetType: TargetCustom, Code: "KRT-CUSTOM-01", Title: "Calibration Tag", Copies: 1},
		},
	})
	if err != nil {
		t.Fatalf("GenerateBatchPDF failed: %v", err)
	}
	verifyValidPDFStructure(t, pdfBytes, 1)

	// Confirm Turkish characters were safely folded and PDF parentheses escaped
	if !strings.Contains(string(pdfBytes), `WS2812B Serit LED Modulu \(10Ohm\)`) {
		t.Errorf("expected WinAnsi-sanitized and PDF-escaped Turkish title in PDF stream")
	}
	if !strings.Contains(string(pdfBytes), "Gelistirme Rafi A1") {
		t.Errorf("expected WinAnsi-sanitized Turkish location title in PDF stream")
	}
}
