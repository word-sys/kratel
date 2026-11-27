package labels

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

const ptPerMM = 72.0 / 25.4

// MMToPt converts millimeters to PDF points (1 inch = 72 pt = 25.4 mm).
func MMToPt(mm float64) float64 {
	return mm * ptPerMM
}

// PDFDocument builds a pure-Go PDF 1.4 vector document with embedded Type1 base-14 fonts
// (Helvetica, Helvetica-Bold, Courier, Courier-Bold) and exact byte-offset xref tables.
type PDFDocument struct {
	Title   string
	Creator string
	pages   []*PDFPage
}

// NewPDFDocument creates a new empty PDF 1.4 document.
func NewPDFDocument(title string) *PDFDocument {
	if strings.TrimSpace(title) == "" {
		title = "Kratel Labels"
	}
	return &PDFDocument{
		Title:   title,
		Creator: "Kratel Inventory Ledger",
	}
}

// AddPage appends a new page with dimensions given in millimeters.
func (d *PDFDocument) AddPage(widthMM, heightMM float64) *PDFPage {
	p := &PDFPage{
		widthPt:  MMToPt(widthMM),
		heightPt: MMToPt(heightMM),
		widthMM:  widthMM,
		heightMM: heightMM,
	}
	d.pages = append(d.pages, p)
	return p
}

// PageCount returns the number of pages currently in the document.
func (d *PDFDocument) PageCount() int {
	return len(d.pages)
}

// PDFFont identifies one of the four built-in PDF Type1 fonts.
type PDFFont string

const (
	FontHelvetica     PDFFont = "F1"
	FontHelveticaBold PDFFont = "F2"
	FontCourier       PDFFont = "F3"
	FontCourierBold   PDFFont = "F4"
)

// PDFPage represents a single PDF page with top-down millimeter drawing primitives.
type PDFPage struct {
	widthPt  float64
	heightPt float64
	widthMM  float64
	heightMM float64
	stream   bytes.Buffer
}

// flipY converts a top-down Y coordinate (mm from top edge) to bottom-up PDF points.
func (p *PDFPage) flipY(yTopMM float64) float64 {
	return p.heightPt - MMToPt(yTopMM)
}

// FillRectMM draws a solid grayscale filled rectangle using top-down millimeter coordinates.
func (p *PDFPage) FillRectMM(xMM, yTopMM, wMM, hMM, gray float64) {
	if wMM <= 0 || hMM <= 0 {
		return
	}
	x := MMToPt(xMM)
	w := MMToPt(wMM)
	h := MMToPt(hMM)
	yBottom := p.heightPt - MMToPt(yTopMM+hMM)
	fmt.Fprintf(&p.stream, "q %.3f g %.3f %.3f %.3f %.3f re f Q\n", gray, x, yBottom, w, h)
}

// StrokeRectMM draws a thin stroked rectangle border (used for sticker sheet cut guides).
func (p *PDFPage) StrokeRectMM(xMM, yTopMM, wMM, hMM, lineWidthPt, gray float64, dashed bool) {
	if wMM <= 0 || hMM <= 0 {
		return
	}
	x := MMToPt(xMM)
	w := MMToPt(wMM)
	h := MMToPt(hMM)
	yBottom := p.heightPt - MMToPt(yTopMM+hMM)
	if dashed {
		fmt.Fprintf(&p.stream, "q %.3f G %.3f w [2 2] 0 d %.3f %.3f %.3f %.3f re S Q\n", gray, lineWidthPt, x, yBottom, w, h)
		return
	}
	fmt.Fprintf(&p.stream, "q %.3f G %.3f w %.3f %.3f %.3f %.3f re S Q\n", gray, lineWidthPt, x, yBottom, w, h)
}

// DrawTextMM renders single-line text at (xMM, baselineYTopMM) with optional centering inside boxWidthMM.
func (p *PDFPage) DrawTextMM(font PDFFont, sizePt, xMM, baselineYTopMM, boxWidthMM float64, alignCenter bool, gray float64, text string) {
	safe := sanitizePDFText(text)
	if safe == "" {
		return
	}

	txMM := xMM
	if alignCenter && boxWidthMM > 0 {
		estWidthPt := estimateTextWidthPt(font, sizePt, safe)
		estWidthMM := estWidthPt / ptPerMM
		txMM = xMM + (boxWidthMM-estWidthMM)/2.0
		if txMM < xMM {
			txMM = xMM
		}
	}

	xPt := MMToPt(txMM)
	yPt := p.flipY(baselineYTopMM)

	fmt.Fprintf(&p.stream, "q %.3f g BT /%s %.2f Tf %.3f %.3f Td (%s) Tj ET Q\n",
		gray, string(font), sizePt, xPt, yPt, escapePDFString(safe),
	)
}

// DrawCode128MM renders a Code128 barcode cleanly inside the box (xMM, yTopMM, wMM, hMM).
func (p *PDFPage) DrawCode128MM(bc *Code128Barcode, xMM, yTopMM, wMM, hMM float64) {
	if bc == nil || bc.TotalModules <= 0 || wMM <= 0 || hMM <= 0 {
		return
	}
	modW := wMM / float64(bc.TotalModules)
	yBottom := p.heightPt - MMToPt(yTopMM+hMM)
	hPt := MMToPt(hMM)

	// Emit all bars in one graphics state with zero-stroke fill for razor-sharp thermal printing
	p.stream.WriteString("q 0.000 g ")
	for _, bar := range bc.Bars {
		bx := MMToPt(xMM + float64(bar.X)*modW)
		bw := MMToPt(float64(bar.Width) * modW)
		fmt.Fprintf(&p.stream, "%.3f %.3f %.3f %.3f re ", bx, yBottom, bw, hPt)
	}
	p.stream.WriteString("f Q\n")
}

// DrawQRMM renders a square QR Code inside a square box of sideMM at (xMM, yTopMM), including quiet zones.
func (p *PDFPage) DrawQRMM(qr *QRCode, xMM, yTopMM, sideMM float64, quietModules int) {
	if qr == nil || qr.Size <= 0 || sideMM <= 0 {
		return
	}
	if quietModules < 0 {
		quietModules = 4
	}
	totalMods := qr.Size + 2*quietModules
	modMM := sideMM / float64(totalMods)
	modPt := MMToPt(modMM)

	p.stream.WriteString("q 0.000 g ")
	for y := 0; y < qr.Size; y++ {
		x := 0
		for x < qr.Size {
			if !qr.Modules[y][x] {
				x++
				continue
			}
			// Coalesce horizontal runs into single PDF rectangles to minimize file size
			runStart := x
			for x < qr.Size && qr.Modules[y][x] {
				x++
			}
			runLen := x - runStart
			rx := MMToPt(xMM + float64(quietModules+runStart)*modMM)
			ryTop := yTopMM + float64(quietModules+y)*modMM
			ryBottom := p.heightPt - MMToPt(ryTop+modMM)
			rw := MMToPt(float64(runLen) * modMM)
			fmt.Fprintf(&p.stream, "%.3f %.3f %.3f %.3f re ", rx, ryBottom, rw, modPt)
		}
	}
	p.stream.WriteString("f Q\n")
}

// Bytes serializes the complete PDF 1.4 file with deterministic object IDs and an exact xref table.
func (d *PDFDocument) Bytes() ([]byte, error) {
	if len(d.pages) == 0 {
		return nil, fmt.Errorf("cannot serialize PDF document with zero pages")
	}

	// Object layout:
	// 1: Catalog
	// 2: Pages
	// 3: Font F1 (Helvetica)
	// 4: Font F2 (Helvetica-Bold)
	// 5: Font F3 (Courier)
	// 6: Font F4 (Courier-Bold)
	// 7: Info
	// 8..: For each page i: (8 + 2*i) = Page object, (9 + 2*i) = Content stream object
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")

	totalObjects := 7 + len(d.pages)*2
	offsets := make([]int, totalObjects+1) // 1-indexed

	writeObj := func(id int, body string) {
		offsets[id] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", id, body)
	}

	var kidsRefs []string
	for i := range d.pages {
		pageObjID := 8 + 2*i
		kidsRefs = append(kidsRefs, fmt.Sprintf("%d 0 R", pageObjID))
	}

	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, fmt.Sprintf("<< /Type /Pages /Kids [ %s ] /Count %d >>", strings.Join(kidsRefs, " "), len(d.pages)))
	writeObj(3, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	writeObj(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	writeObj(6, "<< /Type /Font /Subtype /Type1 /BaseFont /Courier-Bold /Encoding /WinAnsiEncoding >>")
	writeObj(7, fmt.Sprintf("<< /Title (%s) /Creator (%s) /Producer (Kratel Pure-Go PDF Engine) >>",
		escapePDFString(sanitizePDFText(d.Title)),
		escapePDFString(sanitizePDFText(d.Creator)),
	))

	fontDict := "<< /F1 3 0 R /F2 4 0 R /F3 5 0 R /F4 6 0 R >>"
	for i, page := range d.pages {
		pageObjID := 8 + 2*i
		streamObjID := 9 + 2*i
		content := page.stream.Bytes()

		pageBody := fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.3f %.3f] /Resources << /Font %s >> /Contents %d 0 R >>",
			page.widthPt, page.heightPt, fontDict, streamObjID,
		)
		writeObj(pageObjID, pageBody)

		offsets[streamObjID] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n<< /Length %d >>\nstream\n", streamObjID, len(content))
		out.Write(content)
		out.WriteString("endstream\nendobj\n")
	}

	xrefOffset := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", totalObjects+1)
	out.WriteString("0000000000 65535 f \n")
	for id := 1; id <= totalObjects; id++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[id])
	}

	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 7 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		totalObjects+1, xrefOffset,
	)

	return out.Bytes(), nil
}

func escapePDFString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '(', ')':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// sanitizePDFText folds non-WinAnsi Unicode characters (such as Turkish ğ/ş/ı/İ or symbols)
// to clean ASCII/Latin equivalents so PDF base-14 Type1 fonts always render cleanly without missing glyphs.
func sanitizePDFText(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch r {
		case 'ğ':
			b.WriteByte('g')
		case 'Ğ':
			b.WriteByte('G')
		case 'ş':
			b.WriteByte('s')
		case 'Ş':
			b.WriteByte('S')
		case 'ı':
			b.WriteByte('i')
		case 'İ':
			b.WriteByte('I')
		case 'ç':
			b.WriteByte('c')
		case 'Ç':
			b.WriteByte('C')
		case 'ö':
			b.WriteByte('o')
		case 'Ö':
			b.WriteByte('O')
		case 'ü':
			b.WriteByte('u')
		case 'Ü':
			b.WriteByte('U')
		case 'Ω', 'Ω':
			b.WriteString("Ohm")
		case 'µ', 'μ':
			b.WriteByte('u')
		case '▸', '→':
			b.WriteString(" > ")
		case '—', '–':
			b.WriteByte('-')
		default:
			if r >= 32 && r <= 126 {
				b.WriteRune(r)
			} else if r >= 160 && r <= 255 {
				// Valid Latin-1 / WinAnsi printable range
				b.WriteByte(byte(r))
			} else if r == '\t' || r == '\n' || r == '\r' {
				b.WriteByte(' ')
			} else {
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}

// estimateTextWidthPt provides a reliable width estimate for centering and truncating text
// on base-14 Helvetica and Courier fonts.
func estimateTextWidthPt(font PDFFont, sizePt float64, asciiText string) float64 {
	if font == FontCourier || font == FontCourierBold {
		return float64(len(asciiText)) * sizePt * 0.60
	}
	// Average Helvetica character width factor (~0.53 * sizePt)
	return float64(len(asciiText)) * sizePt * 0.53
}

// fitTextToWidthMM truncates text with "..." if needed so it fits cleanly inside maxWidthMM.
func fitTextToWidthMM(font PDFFont, sizePt, maxWidthMM float64, text string) string {
	clean := sanitizePDFText(text)
	if clean == "" || maxWidthMM <= 0 {
		return ""
	}
	maxPt := MMToPt(maxWidthMM)
	if estimateTextWidthPt(font, sizePt, clean) <= maxPt {
		return clean
	}
	for n := len(clean); n > 3; n-- {
		cand := clean[:n-3] + "..."
		if estimateTextWidthPt(font, sizePt, cand) <= maxPt {
			return cand
		}
	}
	return strconv.Itoa(len(clean))
}
