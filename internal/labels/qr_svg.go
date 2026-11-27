package labels

import (
	"fmt"
	"html"
	"strings"
)

// QRSVGOptions configures the visual rendering of a vector SVG QR Code.
type QRSVGOptions struct {
	ModuleSize   float64 // Module square size in SVG units (default 6.0)
	QuietModules int     // Border quiet zone in modules (ISO/IEC 18004 standard: 4)
	Title        string  // Optional title line rendered above the QR matrix
	Caption      string  // Optional monospace caption rendered below the QR matrix
	Foreground   string  // Module color (default "#000000")
	Background   string  // Background color (default "#FFFFFF")
}

// DefaultQRSVGOptions returns crisp, high-contrast QR SVG defaults.
func DefaultQRSVGOptions() QRSVGOptions {
	return QRSVGOptions{
		ModuleSize:   6.0,
		QuietModules: 4,
		Foreground:   "#000000",
		Background:   "#FFFFFF",
	}
}

// RenderQRSVG encodes text as an ISO/IEC 18004 QR Code (Level M) and renders a single-path vector <svg>.
func RenderQRSVG(text string, opts QRSVGOptions) (string, error) {
	qr, err := EncodeQR(text)
	if err != nil {
		return "", err
	}

	if opts.ModuleSize <= 0 {
		opts.ModuleSize = 6.0
	}
	if opts.QuietModules <= 0 {
		opts.QuietModules = 4
	}
	if strings.TrimSpace(opts.Foreground) == "" {
		opts.Foreground = "#000000"
	}
	if strings.TrimSpace(opts.Background) == "" {
		opts.Background = "#FFFFFF"
	}

	matrixPx := float64(qr.Size+2*opts.QuietModules) * opts.ModuleSize
	topExtra := 0.0
	if strings.TrimSpace(opts.Title) != "" {
		topExtra = 18.0
	}
	bottomExtra := 0.0
	if strings.TrimSpace(opts.Caption) != "" {
		bottomExtra = 18.0
	}

	totalWidth := matrixPx
	totalHeight := topExtra + matrixPx + bottomExtra
	quietPx := float64(opts.QuietModules) * opts.ModuleSize

	var sb strings.Builder
	sb.Grow(qr.Size*qr.Size*16 + 512)

	fmt.Fprintf(&sb,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.2f %.2f" width="%.2f" height="%.2f" shape-rendering="crispEdges">`,
		totalWidth, totalHeight, totalWidth, totalHeight,
	)
	fmt.Fprintf(&sb,
		`<rect x="0" y="0" width="%.2f" height="%.2f" fill="%s"/>`,
		totalWidth, totalHeight, html.EscapeString(opts.Background),
	)

	if title := strings.TrimSpace(opts.Title); title != "" {
		fmt.Fprintf(&sb,
			`<text x="%.2f" y="14" text-anchor="middle" font-family="ui-sans-serif,system-ui,sans-serif" font-size="11" font-weight="600" fill="%s">%s</text>`,
			totalWidth/2, html.EscapeString(opts.Foreground), html.EscapeString(title),
		)
	}

	// Merge horizontal dark runs per row into one compact SVG <path>
	sb.WriteString(`<path fill="`)
	sb.WriteString(html.EscapeString(opts.Foreground))
	sb.WriteString(`" d="`)
	ms := opts.ModuleSize
	for y := 0; y < qr.Size; y++ {
		x := 0
		for x < qr.Size {
			if !qr.Modules[y][x] {
				x++
				continue
			}
			start := x
			for x < qr.Size && qr.Modules[y][x] {
				x++
			}
			runW := float64(x-start) * ms
			px := quietPx + float64(start)*ms
			py := topExtra + quietPx + float64(y)*ms
			fmt.Fprintf(&sb, "M%.2f %.2fh%.2fv%.2fh-%.2fz", px, py, runW, ms, runW)
		}
	}
	sb.WriteString(`"/>`)

	if caption := strings.TrimSpace(opts.Caption); caption != "" {
		capY := topExtra + matrixPx + 11.0
		fmt.Fprintf(&sb,
			`<text x="%.2f" y="%.2f" text-anchor="middle" font-family="ui-monospace,SFMono-Regular,Menlo,monospace" font-size="11" fill="%s">%s</text>`,
			totalWidth/2, capY, html.EscapeString(opts.Foreground), html.EscapeString(caption),
		)
	}

	sb.WriteString(`</svg>`)
	return sb.String(), nil
}
