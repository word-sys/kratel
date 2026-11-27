package labels

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
)

const MaxBatchLabels = 1000

// LabelTargetType identifies what kind of entity a label represents.
type LabelTargetType string

const (
	TargetItem     LabelTargetType = "item"
	TargetLocation LabelTargetType = "location"
	TargetKit      LabelTargetType = "kit"
	TargetCustom   LabelTargetType = "custom"
)

// LabelEntryRequest requests 1 or more copies of a label for an Item, Location, Kit, or Custom payload.
type LabelEntryRequest struct {
	TargetType LabelTargetType `json:"target_type"`
	ID         int64           `json:"id,omitempty"`
	Code       string          `json:"code,omitempty"`
	Title      string          `json:"title,omitempty"`
	Subtitle   string          `json:"subtitle,omitempty"`
	Badge      string          `json:"badge,omitempty"`
	Copies     int             `json:"copies,omitempty"`
}

// PDFBatchRequest specifies a batch PDF label generation request for the Label Studio.
type PDFBatchRequest struct {
	Layout      LayoutID            `json:"layout"`
	Style       LabelStyle          `json:"style"`
	ShowBorders *bool               `json:"show_borders,omitempty"`
	Entries     []LabelEntryRequest `json:"entries"`
}

// Service bridges Kratel's Catalog/Locations/Kits with pure-Go SVG and PDF 1.4 label generation.
type Service struct {
	store   *db.Store
	catalog *catalog.Service
}

// NewService constructs a Label Studio service.
func NewService(store *db.Store, cat *catalog.Service) *Service {
	if cat == nil && store != nil {
		cat = catalog.NewService(store)
	}
	return &Service{
		store:   store,
		catalog: cat,
	}
}

// RenderSingleSVG resolves a target entity (item, location, kit, or custom code) and renders
// a standalone crisp vector SVG in either Code128 or QR format.
func (s *Service) RenderSingleSVG(ctx context.Context, req LabelEntryRequest, style LabelStyle) (string, *LabelItem, error) {
	resolved, err := s.resolveEntry(ctx, req)
	if err != nil {
		return "", nil, err
	}

	normStyle := LabelStyle(strings.ToLower(strings.TrimSpace(string(style))))
	if normStyle == "" {
		normStyle = StyleCode128
	}

	switch normStyle {
	case StyleQR:
		opts := DefaultQRSVGOptions()
		opts.Title = resolved.Title
		opts.Caption = resolved.Code
		svg, err := RenderQRSVG(resolved.Code, opts)
		return svg, resolved, err
	case StyleCode128:
		opts := DefaultCode128SVGOptions()
		opts.Title = resolved.Title
		svg, err := RenderCode128SVG(resolved.Code, opts)
		return svg, resolved, err
	default:
		return "", nil, apperr.Validation("style", "label style must be 'code128' or 'qr'")
	}
}

// GenerateBatchPDF resolves all requested items, storage locations, kits, or custom labels,
// expands copy counts, and generates a print-ready PDF 1.4 document.
func (s *Service) GenerateBatchPDF(ctx context.Context, req PDFBatchRequest) ([]byte, error) {
	if len(req.Entries) == 0 {
		return nil, apperr.Validation("entries", "at least one label entry is required")
	}

	expanded := make([]LabelItem, 0, len(req.Entries))
	for _, entry := range req.Entries {
		copies := entry.Copies
		if copies <= 0 {
			copies = 1
		}
		if len(expanded)+copies > MaxBatchLabels {
			return nil, apperr.Validation("entries", fmt.Sprintf("total labels per PDF cannot exceed %d", MaxBatchLabels))
		}

		lbl, err := s.resolveEntry(ctx, entry)
		if err != nil {
			return nil, err
		}
		for c := 0; c < copies; c++ {
			expanded = append(expanded, *lbl)
		}
	}

	return RenderLabelsPDF(expanded, PDFRenderOptions{
		Layout:      req.Layout,
		Style:       req.Style,
		ShowBorders: req.ShowBorders,
		DocTitle:    "Kratel Print Labels",
	})
}

func (s *Service) resolveEntry(ctx context.Context, req LabelEntryRequest) (*LabelItem, error) {
	target := LabelTargetType(strings.ToLower(strings.TrimSpace(string(req.TargetType))))
	if target == "" {
		if req.ID > 0 {
			target = TargetItem
		} else {
			target = TargetCustom
		}
	}

	switch target {
	case TargetItem:
		if s.catalog == nil {
			return nil, apperr.Validation("target_type", "catalog service not configured")
		}
		item, err := s.catalog.GetItemByID(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		code := strings.TrimSpace(item.Barcode)
		if code == "" {
			code = item.SKU
		}
		sub := "SKU: " + item.SKU
		if item.DefaultLocationCode != "" {
			sub += " | Bin: " + item.DefaultLocationCode
		} else if len(item.StockByLocation) > 0 {
			sub += " | Bin: " + item.StockByLocation[0].LocationCode
		}
		badge := item.Category
		if badge != "" && item.Unit != "" {
			badge += " (" + item.Unit + ")"
		} else if item.Unit != "" {
			badge = item.Unit
		}
		return &LabelItem{
			Title:    item.Name,
			Subtitle: sub,
			Code:     code,
			Badge:    badge,
		}, nil

	case TargetLocation:
		if s.catalog == nil {
			return nil, apperr.Validation("target_type", "catalog service not configured")
		}
		loc, err := s.catalog.GetLocationByID(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		return &LabelItem{
			Title:    loc.Name,
			Subtitle: loc.Breadcrumb,
			Code:     loc.ScanBarcode,
			Badge:    "BIN: " + loc.Code,
		}, nil

	case TargetKit:
		if s.store == nil {
			return nil, apperr.Validation("target_type", "database store not configured")
		}
		var code, barcode, name, category string
		err := s.store.DB().QueryRowContext(
			ctx,
			`SELECT code, COALESCE(barcode, ''), name, category FROM kits WHERE id = ?;`,
			req.ID,
		).Scan(&code, &barcode, &name, &category)
		if err == sql.ErrNoRows {
			return nil, apperr.NotFound("kit", fmt.Sprintf("%d", req.ID))
		}
		if err != nil {
			return nil, err
		}
		scanCode := strings.TrimSpace(barcode)
		if scanCode == "" {
			scanCode = catalog.KitBarcodePrefix + strings.ToUpper(code)
		}
		return &LabelItem{
			Title:    name,
			Subtitle: "BOM / Repair Kit: " + code,
			Code:     scanCode,
			Badge:    category,
		}, nil

	case TargetCustom:
		code := strings.TrimSpace(req.Code)
		if code == "" {
			return nil, apperr.Validation("code", "custom label code is required")
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = code
		}
		return &LabelItem{
			Title:    title,
			Subtitle: strings.TrimSpace(req.Subtitle),
			Code:     code,
			Badge:    strings.TrimSpace(req.Badge),
		}, nil

	default:
		return nil, apperr.Validation("target_type", fmt.Sprintf("unsupported label target_type %q", req.TargetType))
	}
}
