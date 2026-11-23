package catalog

import (
	"context"
	"sort"
	"strings"

	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

// ListLowStockReorderQueue returns all active items whose total stock across locations
// is at or below min_stock, enriched with deficit, suggested order quantity, and estimated restock cost.
func (s *Service) ListLowStockReorderQueue(ctx context.Context) ([]models.ReorderItem, error) {
	items, _, err := s.ListItems(ctx, ItemFilter{
		LowStockOnly:    true,
		IncludeArchived: false,
	})
	if err != nil {
		return nil, err
	}

	// Preload supplier contacts for reorder sheets
	suppliers, err := s.ListSuppliers(ctx)
	if err != nil {
		return nil, err
	}
	supByID := make(map[int64]models.Supplier, len(suppliers))
	for _, sup := range suppliers {
		supByID[sup.ID] = sup
	}

	queue := make([]models.ReorderItem, 0, len(items))
	for _, it := range items {
		deficit := it.MinStockMillis.Sub(it.TotalStockMillis)
		if deficit < 0 {
			deficit = 0
		}

		// Suggested restock targets 2x min_stock (to build a buffer above the alert threshold),
		// or at least min_stock / 1 unit if min_stock == 0.
		targetMillis := it.MinStockMillis * 2
		if targetMillis <= 0 {
			targetMillis = num.FromUnits(1)
		}
		suggested := targetMillis.Sub(it.TotalStockMillis)
		if suggested < deficit {
			suggested = deficit
		}
		if suggested <= 0 {
			suggested = num.FromUnits(1)
		}

		estCost := num.LineValuationSafe(suggested, it.UnitCostMillis)

		primaryLoc := it.DefaultLocationCode
		if primaryLoc == "" && len(it.StockByLocation) > 0 {
			primaryLoc = it.StockByLocation[0].LocationCode
		}
		if primaryLoc == "" {
			primaryLoc = db.DefaultLocationCode
		}

		supName := it.SupplierName
		supContact := ""
		if it.SupplierID != nil {
			if sup, ok := supByID[*it.SupplierID]; ok {
				supName = sup.Name
				parts := []string{}
				if sup.Contact != "" {
					parts = append(parts, sup.Contact)
				}
				if sup.Phone != "" {
					parts = append(parts, sup.Phone)
				}
				if sup.Email != "" {
					parts = append(parts, sup.Email)
				}
				supContact = strings.Join(parts, " | ")
			}
		}

		queue = append(queue, models.ReorderItem{
			ItemID:                 it.ID,
			SKU:                    it.SKU,
			Barcode:                it.Barcode,
			Name:                   it.Name,
			Category:               it.Category,
			Unit:                   it.Unit,
			PrimaryLocationCode:    primaryLoc,
			SupplierID:             it.SupplierID,
			SupplierName:           supName,
			SupplierContact:        supContact,
			CurrentStockMillis:     it.TotalStockMillis,
			CurrentStockText:       it.TotalStockText,
			MinStockMillis:         it.MinStockMillis,
			MinStockText:           it.MinStockText,
			DeficitMillis:          deficit,
			DeficitText:            deficit.Format(),
			SuggestedOrderMillis:   suggested,
			SuggestedOrderText:     suggested.Format(),
			UnitCostMillis:         it.UnitCostMillis,
			UnitCostText:           it.UnitCostText,
			EstimatedRestockMillis: estCost,
			EstimatedRestockText:   estCost.FormatFixed(2),
		})
	}

	sort.Slice(queue, func(i, j int) bool {
		if queue[i].DeficitMillis != queue[j].DeficitMillis {
			return queue[i].DeficitMillis > queue[j].DeficitMillis
		}
		return queue[i].SKU < queue[j].SKU
	})

	return queue, nil
}

// BuildSupplierReorderGroups groups all low-stock items by supplier for purchase order checklists.
func (s *Service) BuildSupplierReorderGroups(ctx context.Context) ([]models.SupplierReorderGroup, error) {
	queue, err := s.ListLowStockReorderQueue(ctx)
	if err != nil {
		return nil, err
	}

	suppliers, err := s.ListSuppliers(ctx)
	if err != nil {
		return nil, err
	}
	supByID := make(map[int64]models.Supplier, len(suppliers))
	for _, sup := range suppliers {
		supByID[sup.ID] = sup
	}

	groupsByID := make(map[int64]*models.SupplierReorderGroup)
	var order []int64

	for _, item := range queue {
		var supID int64
		if item.SupplierID != nil {
			supID = *item.SupplierID
		}
		grp, exists := groupsByID[supID]
		if !exists {
			grp = &models.SupplierReorderGroup{
				SupplierID:   supID,
				SupplierName: "Unassigned Supplier",
				Items:        []models.ReorderItem{},
			}
			if sup, ok := supByID[supID]; ok {
				grp.SupplierName = sup.Name
				grp.SupplierContact = sup.Contact
				grp.SupplierEmail = sup.Email
				grp.SupplierPhone = sup.Phone
			}
			groupsByID[supID] = grp
			order = append(order, supID)
		}

		grp.Items = append(grp.Items, item)
		grp.TotalItems++
		grp.EstimatedCostMillis = grp.EstimatedCostMillis.Add(item.EstimatedRestockMillis)
		grp.EstimatedCostText = grp.EstimatedCostMillis.FormatFixed(2)
	}

	out := make([]models.SupplierReorderGroup, 0, len(order))
	for _, id := range order {
		out = append(out, *groupsByID[id])
	}

	sort.Slice(out, func(i, j int) bool {
		// Assigned suppliers first, unassigned (0) last
		if (out[i].SupplierID == 0) != (out[j].SupplierID == 0) {
			return out[i].SupplierID != 0
		}
		return strings.ToLower(out[i].SupplierName) < strings.ToLower(out[j].SupplierName)
	})

	return out, nil
}

// ListExpiringItems returns active items whose expiry_date is expired or within warningDays (default 30),
// ordered chronologically by expiry_date ascending.
func (s *Service) ListExpiringItems(ctx context.Context, warningDays int) ([]models.Item, error) {
	if warningDays <= 0 {
		warningDays = 30
	}
	items, _, err := s.ListItems(ctx, ItemFilter{
		IncludeArchived: false,
	})
	if err != nil {
		return nil, err
	}

	var expiring []models.Item
	for _, it := range items {
		if it.ExpiryDate == "" {
			continue
		}
		status := evaluateExpiryStatus(it.ExpiryDate, warningDays)
		if status == "EXPIRED" || status == "EXPIRING_SOON" {
			it.ExpiryStatus = status
			expiring = append(expiring, it)
		}
	}

	sort.Slice(expiring, func(i, j int) bool {
		if expiring[i].ExpiryDate != expiring[j].ExpiryDate {
			return expiring[i].ExpiryDate < expiring[j].ExpiryDate
		}
		return expiring[i].SKU < expiring[j].SKU
	})

	return expiring, nil
}
