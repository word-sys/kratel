package labels

import (
	"fmt"
	"html"
	"strings"
)

// Code128SVGOptions controls the rendered SVG geometry and optional human-readable text.
type Code128SVGOptions struct {
	ModuleWidth float64 // Width of a 1-module bar in SVG units (default 2.0)
	BarHeight   float64 // Height of barcode bars in SVG units (default 56.0)
	ShowText    bool    // Render human-readable monospace caption beneath the bars
	Title       string  // Optional title line rendered above the barcode
	Foreground  string  // Bar & text color (default "#000000")
	Background  string  // Canvas background color (default "#FFFFFF")
}

// DefaultCode128SVGOptions returns crisp defaults suitable for screen previews and print sheets.
func DefaultCode128SVGOptions() Code128SVGOptions {
	return Code128SVGOptions{
		ModuleWidth: 2.0,
		BarHeight:   56.0,
		ShowText:    true,
		Foreground:  "#000000",
		Background:  "#FFFFFF",
	}
}

// RenderCode128SVG encodes text as Code128 and renders a self-contained vector <svg> document.
func RenderCode128SVG(text string, opts Code128SVGOptions) (string, error) {
	bc, err := EncodeCode128(text)
	if err != nil {
		return "", err
	}

	if opts.ModuleWidth <= 0 {
		opts.ModuleWidth = 2.0
	}
	if opts.BarHeight <= 0 {
		opts.BarHeight = 56.0
	}
	if strings.TrimSpace(opts.Foreground) == "" {
		opts.Foreground = "#000000"
	}
	if strings.TrimSpace(opts.Background) == "" {
		opts.Background = "#FFFFFF"
	}

	totalWidth := float64(bc.TotalModules) * opts.ModuleWidth
	topPad := 6.0
	if strings.TrimSpace(opts.Title) != "" {
		topPad = 22.0
	}
	bottomPad := 6.0
	if opts.ShowText {
		bottomPad = 20.0
	}
	totalHeight := topPad + opts.BarHeight + bottomPad

	var sb strings.Builder
	sb.Grow(len(bc.Bars)*36 + 512)

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
			`<text x="%.2f" y="15" text-anchor="middle" font-family="ui-sans-serif,system-ui,sans-serif" font-size="11" font-weight="600" fill="%s">%s</text>`,
			totalWidth/2, html.EscapeString(opts.Foreground), html.EscapeString(title),
		)
	}

	// Render all dark bars in a single compact <path> for fast browser layout and minimal payload size
	sb.WriteString(`<path fill="`)
	sb.WriteString(html.EscapeString(opts.Foreground))
	sb.WriteString(`" d="`)
	for _, bar := range bc.Bars {
		bx := float64(bar.X) * opts.ModuleWidth
		bw := float64(bar.Width) * opts.ModuleWidth
		fmt.Fprintf(&sb, "M%.2f %.2fh%.2fv%.2fh-%.2fz", bx, topPad, bw, opts.BarHeight, bw)
	}
	sb.WriteString(`"/>`)

	if opts.ShowText {
		textY := topPad + opts.BarHeight + 14.0
		fmt.Fprintf(&sb,
			`<text x="%.2f" y="%.2f" text-anchor="middle" font-family="ui-monospace,SFMono-Regular,Menlo,monospace" font-size="12" letter-spacing="1" fill="%s">%s</text>`,
			totalWidth/2, textY, html.EscapeString(opts.Foreground), html.EscapeString(bc.Text),
		)
	}

	sb.WriteString(`</svg>`)
	return sb.String(), nil
}
