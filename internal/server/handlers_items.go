package server

import (
	"net/http"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/ledger"
	"github.com/word-sys/kratel/internal/models"
)

func (s *Server) handleItemsCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		filter := catalog.ItemFilter{
			Query:           strings.TrimSpace(r.URL.Query().Get("q")),
			Category:        strings.TrimSpace(r.URL.Query().Get("category")),
			LocationID:      parseQueryInt64(r, "location_id"),
			SupplierID:      parseQueryInt64(r, "supplier_id"),
			LowStockOnly:    parseQueryBool(r, "low_stock"),
			ExpiringOnly:    parseQueryBool(r, "expiring"),
			IncludeArchived: parseQueryBool(r, "include_archived"),
			AttributeKey:    strings.TrimSpace(r.URL.Query().Get("attr_key")),
			AttributeValue:  strings.TrimSpace(r.URL.Query().Get("attr_val")),
			Limit:           parseQueryInt(r, "limit", 0),
			Offset:          parseQueryInt(r, "offset", 0),
		}
		items, total, err := s.catalog.ListItems(ctx, filter)
		if err != nil {
			writeError(w, err)
			return
		}
		if items == nil {
			items = []models.Item{}
		}
		categories, _ := s.catalog.ListCategories(ctx)
		if categories == nil {
			categories = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items":      items,
			"total":      total,
			"categories": categories,
		})

	case http.MethodPost:
		var in models.ItemInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		in.Operator = requestOperator(r, in.Operator)
		created, err := s.catalog.CreateItem(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleAttributesDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, apperr.Validation("method", "GET required"))
		return
	}
	facets, err := catalog.DiscoverAttributeKeys(r.Context(), s.store.DB())
	if err != nil {
		writeError(w, err)
		return
	}
	if facets == nil {
		facets = []catalog.AttributeFacet{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"attributes": facets,
	})
}

func (s *Server) handleItemResource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/items/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, apperr.NotFound("item", ""))
		return
	}

	// GET /api/v1/items/lookup?code=...
	if parts[0] == "lookup" && r.Method == http.MethodGet {
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		item, src, err := s.catalog.ResolveItemByCode(ctx, code)
		if err != nil {
			writeError(w, err)
			return
		}
		recent, _, _ := s.ledger.ListTransactions(ctx, ledger.TransactionFilter{ItemID: item.ID, Limit: 15})
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"item":                item,
			"match_source":        src,
			"recent_transactions": recent,
		})
		return
	}

	itemID, err := parseIDParam(parts[0], "item_id")
	if err != nil {
		writeError(w, err)
		return
	}

	// /api/v1/items/{id}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			item, err := s.catalog.GetItemByID(ctx, itemID)
			if err != nil {
				writeError(w, err)
				return
			}
			txs, _, _ := s.ledger.ListTransactions(ctx, ledger.TransactionFilter{ItemID: itemID, Limit: 50})
			if txs == nil {
				txs = []models.Transaction{}
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"item":         item,
				"transactions": txs,
			})

		case http.MethodPut:
			var in models.ItemInput
			if err := decodeJSON(r, &in); err != nil {
				writeError(w, err)
				return
			}
			updated, err := s.catalog.UpdateItem(ctx, itemID, in)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, updated)

		case http.MethodDelete:
			// Support ?archive=true to soft-archive items with ledger history
			if parseQueryBool(r, "archive") {
				archived, err := s.catalog.ArchiveItem(ctx, itemID, true)
				if err != nil {
					writeError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, archived)
				return
			}
			if err := s.catalog.DeleteItem(ctx, itemID); err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})

		default:
			writeError(w, apperr.Validation("method", "method not allowed"))
		}
		return
	}

	// /api/v1/items/{id}/barcode
	if len(parts) == 2 && parts[1] == "barcode" && r.Method == http.MethodPost {
		updated, err := s.catalog.AssignInternalBarcode(ctx, itemID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
		return
	}

	// /api/v1/items/{id}/archive
	if len(parts) == 2 && parts[1] == "archive" && r.Method == http.MethodPost {
		var body struct {
			Archived bool `json:"archived"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, err)
			return
		}
		updated, err := s.catalog.ArchiveItem(ctx, itemID, body.Archived)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
		return
	}

	// /api/v1/items/{id}/aliases
	if len(parts) == 2 && parts[1] == "aliases" {
		switch r.Method {
		case http.MethodGet:
			aliases, err := s.catalog.ListItemAliases(ctx, itemID)
			if err != nil {
				writeError(w, err)
				return
			}
			if aliases == nil {
				aliases = []models.ItemAlias{}
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"aliases": aliases})

		case http.MethodPost:
			var body struct {
				Code      string           `json:"code"`
				AliasType models.AliasType `json:"alias_type"`
				Notes     string           `json:"notes"`
			}
			if err := decodeJSON(r, &body); err != nil {
				writeError(w, err)
				return
			}
			created, err := s.catalog.AddItemAlias(ctx, itemID, body.Code, body.AliasType, body.Notes)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, created)

		default:
			writeError(w, apperr.Validation("method", "method not allowed"))
		}
		return
	}

	// DELETE /api/v1/items/{id}/aliases/{aliasID}
	if len(parts) == 3 && parts[1] == "aliases" && r.Method == http.MethodDelete {
		aliasID, err := parseIDParam(parts[2], "alias_id")
		if err != nil {
			writeError(w, err)
			return
		}
		if err := s.catalog.DeleteItemAlias(ctx, aliasID); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
		return
	}

	writeError(w, apperr.NotFound("endpoint", r.URL.Path))
}
