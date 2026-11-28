package server

import (
	"net/http"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/models"
)

func (s *Server) handleLocationsCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		flat, err := s.catalog.ListLocations(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		if flat == nil {
			flat = []models.Location{}
		}
		tree, err := s.catalog.BuildLocationTree(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		if tree == nil {
			tree = []*models.LocationTreeNode{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"locations": flat,
			"tree":      tree,
		})

	case http.MethodPost:
		var in catalog.LocationInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		created, err := s.catalog.CreateLocation(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleLocationResource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/locations/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, apperr.NotFound("location", ""))
		return
	}

	locID, err := parseIDParam(parts[0], "location_id")
	if err != nil {
		writeError(w, err)
		return
	}

	// GET /api/v1/locations/{id}/items
	if len(parts) == 2 && parts[1] == "items" && r.Method == http.MethodGet {
		loc, err := s.catalog.GetLocationByID(ctx, locID)
		if err != nil {
			writeError(w, err)
			return
		}
		items, total, err := s.catalog.ListItems(ctx, catalog.ItemFilter{LocationID: locID})
		if err != nil {
			writeError(w, err)
			return
		}
		if items == nil {
			items = []models.Item{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"location": loc,
			"items":    items,
			"total":    total,
		})
		return
	}

	if len(parts) != 1 {
		writeError(w, apperr.NotFound("endpoint", r.URL.Path))
		return
	}

	switch r.Method {
	case http.MethodGet:
		loc, err := s.catalog.GetLocationByID(ctx, locID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, loc)

	case http.MethodPut:
		var in catalog.LocationInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		updated, err := s.catalog.UpdateLocation(ctx, locID, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if err := s.catalog.DeleteLocation(ctx, locID); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleSuppliersCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		suppliers, err := s.catalog.ListSuppliers(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		if suppliers == nil {
			suppliers = []models.Supplier{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"suppliers": suppliers,
		})

	case http.MethodPost:
		var in catalog.SupplierInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		created, err := s.catalog.CreateSupplier(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleSupplierResource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/suppliers/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, apperr.NotFound("supplier", ""))
		return
	}

	// GET /api/v1/suppliers/reorder-list
	if parts[0] == "reorder-list" && r.Method == http.MethodGet {
		queue, err := s.catalog.ListLowStockReorderQueue(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		if queue == nil {
			queue = []models.ReorderItem{}
		}
		groups, err := s.catalog.BuildSupplierReorderGroups(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		if groups == nil {
			groups = []models.SupplierReorderGroup{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"queue":  queue,
			"groups": groups,
		})
		return
	}

	supID, err := parseIDParam(parts[0], "supplier_id")
	if err != nil {
		writeError(w, err)
		return
	}

	if len(parts) != 1 {
		writeError(w, apperr.NotFound("endpoint", r.URL.Path))
		return
	}

	switch r.Method {
	case http.MethodGet:
		sup, err := s.catalog.GetSupplierByID(ctx, supID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sup)

	case http.MethodPut:
		var in catalog.SupplierInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		updated, err := s.catalog.UpdateSupplier(ctx, supID, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if err := s.catalog.DeleteSupplier(ctx, supID); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}
