package labels

import (
	"fmt"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
)

// LabelStyle selects the machine-readable symbology printed on each label.
type LabelStyle string

const (
	StyleCode128 LabelStyle = "code128"
	StyleQR      LabelStyle = "qr"
)

// LayoutID identifies a physical thermal roll or sticker sheet format.
type LayoutID string

const (
	LayoutThermal50x25 LayoutID = "thermal_50x25"
	LayoutThermal40x30 LayoutID = "thermal_40x30"
	LayoutA4_3x8       LayoutID = "a4_3x8"
	LayoutLetter3x10   LayoutID = "letter_3x10"
)

// LayoutSpec describes the exact millimeter geometry of a thermal label or sticker sheet.
type LayoutSpec struct {
	ID           LayoutID `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	PageWidthMM  float64  `json:"page_width_mm"`
	PageHeightMM float64  `json:"page_height_mm"`
	Cols         int      `json:"cols"`
	Rows         int      `json:"rows"`
	LabelsPerPg  int      `json:"labels_per_page"`
	MarginLeftMM float64  `json:"margin_left_mm"`
	MarginTopMM  float64  `json:"margin_top_mm"`
	LabelWidthMM float64  `json:"label_width_mm"`
	LabelHeightMM float64 `json:"label_height_mm"`
	ColGapMM     float64  `json:"col_gap_mm"`
	RowGapMM     float64  `json:"row_gap_mm"`
	IsThermal    bool     `json:"is_thermal"`
}

var supportedLayouts = []LayoutSpec{
	{
		ID:            LayoutThermal50x25,
		Name:          "Thermal Roll 50 × 25 mm",
		Description:   "Standard desktop thermal label roll (1 label per page)",
		PageWidthMM:   50.0,
		PageHeightMM:  25.0,
		Cols:          1,
		Rows:          1,
		LabelsPerPg:   1,
		MarginLeftMM:  0.0,
		MarginTopMM:   0.0,
		LabelWidthMM:  50.0,
		LabelHeightMM: 25.0,
		IsThermal:     true,
	},
	{
		ID:            LayoutThermal40x30,
		Name:          "Thermal Roll 40 × 30 mm",
		Description:   "Square-ish thermal bin & part label roll (1 label per page)",
		PageWidthMM:   40.0,
		PageHeightMM:  30.0,
		Cols:          1,
		Rows:          1,
		LabelsPerPg:   1,
		MarginLeftMM:  0.0,
		MarginTopMM:   0.0,
		LabelWidthMM:  40.0,
		LabelHeightMM: 30.0,
		IsThermal:     true,
	},
	{
		ID:            LayoutA4_3x8,
		Name:          "A4 Sticker Sheet (3 × 8 / 24-up)",
		Description:   "210 × 297 mm A4 sheet, 24 labels per page (64 × 33.8 mm)",
		PageWidthMM:   210.0,
		PageHeightMM:  297.0,
		Cols:          3,
		Rows:          8,
		LabelsPerPg:   24,
		MarginLeftMM:  7.0,
		MarginTopMM:   13.3,
		LabelWidthMM:  64.0,
		LabelHeightMM: 33.8,
		ColGapMM:      2.0,
		RowGapMM:      0.0,
		IsThermal:     false,
	},
	{
		ID:            LayoutLetter3x10,
		Name:          "US Letter Sticker Sheet (3 × 10 / 30-up)",
		Description:   "8.5 × 11 in Letter sheet (Avery 5160 compatible), 30 labels per page",
		PageWidthMM:   215.9,
		PageHeightMM:  279.4,
		Cols:          3,
		Rows:          10,
		LabelsPerPg:   30,
		MarginLeftMM:  4.7625,
		MarginTopMM:   12.7,
		LabelWidthMM:  66.675,
		LabelHeightMM: 25.4,
		ColGapMM:      3.175,
		RowGapMM:      0.0,
		IsThermal:     false,
	},
}

// SupportedLayouts returns all built-in label sheet and thermal roll specifications.
func SupportedLayouts() []LayoutSpec {
	out := make([]LayoutSpec, len(supportedLayouts))
	copy(out, supportedLayouts)
	return out
}

// GetLayoutSpec resolves a LayoutID string (defaulting to LayoutThermal50x25 if empty).
func GetLayoutSpec(id LayoutID) (LayoutSpec, error) {
	norm := LayoutID(strings.ToLower(strings.TrimSpace(string(id))))
	if norm == "" {
		norm = LayoutThermal50x25
	}
	for _, spec := range supportedLayouts {
		if spec.ID == norm {
			return spec, nil
		}
	}
	return LayoutSpec{}, apperr.Validation("layout", fmt.Sprintf("unsupported label layout %q", id))
}

// LabelItem holds the resolved text and barcode payload for one physical sticker.
type LabelItem struct {
	Title    string `json:"title"`    // Primary human label (e.g. "ESP32-S3 DevKitC")
	Subtitle string `json:"subtitle"` // Secondary line (e.g. "SKU: MCU-ESP32  |  Bin: A-01")
	Code     string `json:"code"`     // Machine-scannable payload (e.g. "8690001001" or "LOC:A-01")
	Badge    string `json:"badge"`    // Optional right-aligned unit/category tag (e.g. "pcs")
}

// PDFRenderOptions controls layout, symbology, and cut guides when generating a label PDF.
type PDFRenderOptions struct {
	Layout      LayoutID   `json:"layout"`
	Style       LabelStyle `json:"style"`
	ShowBorders *bool      `json:"show_borders,omitempty"`
	DocTitle    string     `json:"doc_title,omitempty"`
}

// RenderLabelsPDF generates a print-ready PDF 1.4 byte slice for the provided labels
// using exact vector Code128 bars or vector QR matrices.
func RenderLabelsPDF(labels []LabelItem, opts PDFRenderOptions) ([]byte, error) {
	if len(labels) == 0 {
		return nil, apperr.Validation("labels", "at least one label is required to generate a PDF")
	}

	spec, err := GetLayoutSpec(opts.Layout)
	if err != nil {
		return nil, err
	}

	style := LabelStyle(strings.ToLower(strings.TrimSpace(string(opts.Style))))
	if style == "" {
		style = StyleCode128
	}
	if style != StyleCode128 && style != StyleQR {
		return nil, apperr.Validation("style", "label style must be 'code128' or 'qr'")
	}

	showBorders := !spec.IsThermal
	if opts.ShowBorders != nil {
		showBorders = *opts.ShowBorders
	}

	doc := NewPDFDocument(opts.DocTitle)
	var curPage *PDFPage

	for idx, lbl := range labels {
		slot := idx % spec.LabelsPerPg
		if slot == 0 {
			curPage = doc.AddPage(spec.PageWidthMM, spec.PageHeightMM)
		}

		col := slot % spec.Cols
		row := slot / spec.Cols

		cellX := spec.MarginLeftMM + float64(col)*(spec.LabelWidthMM+spec.ColGapMM)
		cellY := spec.MarginTopMM + float64(row)*(spec.LabelHeightMM+spec.RowGapMM)

		if showBorders {
			curPage.StrokeRectMM(cellX, cellY, spec.LabelWidthMM, spec.LabelHeightMM, 0.35, 0.78, !spec.IsThermal)
		}

		if err := drawLabelCell(curPage, cellX, cellY, spec.LabelWidthMM, spec.LabelHeightMM, lbl, style); err != nil {
			return nil, err
		}
	}

	return doc.Bytes()
}

func drawLabelCell(page *PDFPage, xMM, yTopMM, wMM, hMM float64, lbl LabelItem, style LabelStyle) error {
	code := strings.TrimSpace(lbl.Code)
	if code == "" {
		return apperr.Validation("code", "label barcode value cannot be empty")
	}

	padX := 2.2
	padY := 2.0
	innerX := xMM + padX
	innerW := wMM - 2*padX

	if style == StyleQR {
		qr, err := EncodeQR(code)
		if err != nil {
			return err
		}
		// Place square QR code on the left side of the label; text stack on the right
		qrSide := hMM - 2*padY
		if qrSide > wMM*0.44 {
			qrSide = wMM * 0.44
		}
		qrY := yTopMM + (hMM-qrSide)/2.0
		page.DrawQRMM(qr, innerX, qrY, qrSide, 2)

		textX := innerX + qrSide + 2.0
		textW := (xMM + wMM - padX) - textX
		if textW > 6.0 {
			titleSize := 7.5
			if hMM >= 30.0 {
				titleSize = 8.5
			}
			titleLine := fitTextToWidthMM(FontHelveticaBold, titleSize, textW, lbl.Title)
			page.DrawTextMM(FontHelveticaBold, titleSize, textX, yTopMM+padY+3.2, textW, false, 0.05, titleLine)

			if sub := strings.TrimSpace(lbl.Subtitle); sub != "" {
				subLine := fitTextToWidthMM(FontHelvetica, 6.2, textW, sub)
				page.DrawTextMM(FontHelvetica, 6.2, textX, yTopMM+padY+6.8, textW, false, 0.35, subLine)
			}

			codeLine := fitTextToWidthMM(FontCourierBold, 6.8, textW, code)
			page.DrawTextMM(FontCourierBold, 6.8, textX, yTopMM+hMM-padY-1.0, textW, false, 0.0, codeLine)

			if badge := strings.TrimSpace(lbl.Badge); badge != "" {
				badgeLine := fitTextToWidthMM(FontHelvetica, 5.8, textW, badge)
				page.DrawTextMM(FontHelvetica, 5.8, textX, yTopMM+padY+10.2, textW, false, 0.40, badgeLine)
			}
		}
		return nil
	}

	// Code128 layout: Title at top, crisp horizontal Code128 bars in center, monospace code at bottom
	bc, err := EncodeCode128(code)
	if err != nil {
		return err
	}

	titleSize := 7.5
	if hMM >= 30.0 {
		titleSize = 8.5
	}
	titleText := fitTextToWidthMM(FontHelveticaBold, titleSize, innerW, lbl.Title)
	page.DrawTextMM(FontHelveticaBold, titleSize, innerX, yTopMM+padY+2.6, innerW, true, 0.05, titleText)

	subOffset := 0.0
	if sub := strings.TrimSpace(lbl.Subtitle); sub != "" {
		subLine := fitTextToWidthMM(FontHelvetica, 5.8, innerW, sub)
		page.DrawTextMM(FontHelvetica, 5.8, innerX, yTopMM+padY+5.5, innerW, true, 0.35, subLine)
		subOffset = 2.6
	}

	barTop := yTopMM + padY + 4.0 + subOffset
	bottomTextH := 3.6
	barH := (yTopMM + hMM - padY - bottomTextH) - barTop
	if barH < 6.0 {
		barH = 6.0
	}

	page.DrawCode128MM(bc, innerX, barTop, innerW, barH)

	captionSize := 6.8
	captionLine := fitTextToWidthMM(FontCourierBold, captionSize, innerW, bc.Text)
	page.DrawTextMM(FontCourierBold, captionSize, innerX, yTopMM+hMM-padY-0.4, innerW, true, 0.0, captionLine)

	return nil
}
