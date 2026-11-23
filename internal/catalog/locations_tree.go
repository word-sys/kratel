package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

// ListLocations returns all storage locations enriched with breadcrumb paths, hierarchy depth,
// scannable LOC:<CODE> barcodes, distinct stocked item counts, and direct stock totals.
func (s *Service) ListLocations(ctx context.Context) ([]models.Location, error) {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT
			l.id, l.code, l.name, l.parent_id, l.notes, l.created_at,
			COUNT(DISTINCT CASE WHEN sl.quantity_millis != 0 THEN sl.item_id END) AS distinct_items,
			COALESCE(SUM(sl.quantity_millis), 0) AS direct_stock_millis
		 FROM locations l
		 LEFT JOIN stock_levels sl ON sl.location_id = l.id
		 GROUP BY l.id, l.code, l.name, l.parent_id, l.notes, l.created_at
		 ORDER BY l.code COLLATE NOCASE ASC, l.id ASC;`,
	)
	if err != nil {
		return nil, fmt.Errorf("list locations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var list []models.Location
	byID := make(map[int64]*models.Location)

	for rows.Next() {
		var (
			loc         models.Location
			parentID    sql.NullInt64
			directStock int64
		)
		if err := rows.Scan(
			&loc.ID,
			&loc.Code,
			&loc.Name,
			&parentID,
			&loc.Notes,
			&loc.CreatedAt,
			&loc.DistinctItems,
			&directStock,
		); err != nil {
			return nil, fmt.Errorf("scan location row: %w", err)
		}
		if parentID.Valid {
			p := parentID.Int64
			loc.ParentID = &p
		}
		loc.ScanBarcode = FormatLocationBarcode(loc.Code)
		loc.DirectStockMillis = num.Millis(directStock)
		loc.DirectStockText = loc.DirectStockMillis.Format()
		list = append(list, loc)
	}

	for i := range list {
		byID[list[i].ID] = &list[i]
	}

	// Compute full breadcrumb paths (e.g. "Workshop ▸ Rack A ▸ Bin A-01") and depth
	for i := range list {
		loc := &list[i]
		if loc.ParentID != nil {
			if parent, ok := byID[*loc.ParentID]; ok {
				loc.ParentCode = parent.Code
			}
		}
		parts := []string{loc.Name}
		seen := map[int64]bool{loc.ID: true}
		cur := loc.ParentID
		depth := 0
		for cur != nil {
			parent, ok := byID[*cur]
			if !ok || seen[parent.ID] {
				break
			}
			seen[parent.ID] = true
			parts = append([]string{parent.Name}, parts...)
			depth++
			cur = parent.ParentID
		}
		loc.Depth = depth
		loc.Breadcrumb = strings.Join(parts, " ▸ ")
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].Breadcrumb != list[j].Breadcrumb {
			return strings.ToLower(list[i].Breadcrumb) < strings.ToLower(list[j].Breadcrumb)
		}
		return list[i].Code < list[j].Code
	})

	return list, nil
}

// BuildLocationTree constructs a nested hierarchical storage tree and performs a post-order
// rollup of subtree item counts and total unit stock quantities across rooms, racks, shelves, and bins.
func (s *Service) BuildLocationTree(ctx context.Context) ([]*models.LocationTreeNode, error) {
	flat, err := s.ListLocations(ctx)
	if err != nil {
		return nil, err
	}

	nodesByID := make(map[int64]*models.LocationTreeNode, len(flat))
	allNodes := make([]*models.LocationTreeNode, 0, len(flat))

	for _, loc := range flat {
		node := &models.LocationTreeNode{
			Location: loc,
			Children: []*models.LocationTreeNode{},
		}
		nodesByID[loc.ID] = node
		allNodes = append(allNodes, node)
	}

	var roots []*models.LocationTreeNode
	for _, node := range allNodes {
		if node.ParentID != nil {
			if parent, ok := nodesByID[*node.ParentID]; ok && parent.ID != node.ID {
				parent.Children = append(parent.Children, node)
				continue
			}
		}
		roots = append(roots, node)
	}

	visited := make(map[int64]bool, len(allNodes))
	var rollup func(n *models.LocationTreeNode)
	rollup = func(n *models.LocationTreeNode) {
		if visited[n.ID] {
			return
		}
		visited[n.ID] = true

		n.SubtreeItems = n.DistinctItems
		n.SubtreeStockMillis = n.DirectStockMillis

		for _, child := range n.Children {
			rollup(child)
			n.SubtreeItems += child.SubtreeItems
			n.SubtreeStockMillis = n.SubtreeStockMillis.Add(child.SubtreeStockMillis)
		}
		n.SubtreeStockText = n.SubtreeStockMillis.Format()
	}

	for _, root := range roots {
		rollup(root)
	}

	return roots, nil
}
