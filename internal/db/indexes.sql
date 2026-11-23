-- Kratel (kratel) High-Cardinality B-Tree Indexes
-- Tuned for sub-10ms catalog filtering, sub-5ms Quick-Scan barcode resolution,
-- and fast ledger history queries across 50,000+ SKUs and 500,000+ transactions.

CREATE INDEX IF NOT EXISTS idx_items_sku ON items(sku);
CREATE INDEX IF NOT EXISTS idx_items_barcode ON items(barcode) WHERE barcode IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_items_category_archived ON items(category, archived);
CREATE INDEX IF NOT EXISTS idx_items_supplier ON items(supplier_id) WHERE supplier_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_items_expiry ON items(expiry_date) WHERE expiry_date != '';

CREATE INDEX IF NOT EXISTS idx_item_aliases_code ON item_aliases(code);
CREATE INDEX IF NOT EXISTS idx_item_aliases_item_id ON item_aliases(item_id);

CREATE INDEX IF NOT EXISTS idx_locations_code ON locations(code);
CREATE INDEX IF NOT EXISTS idx_locations_parent_id ON locations(parent_id);

CREATE INDEX IF NOT EXISTS idx_stock_levels_location_item ON stock_levels(location_id, item_id);
CREATE INDEX IF NOT EXISTS idx_stock_levels_item_qty ON stock_levels(item_id, quantity_millis);

CREATE INDEX IF NOT EXISTS idx_transactions_created_id ON transactions(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_item_created ON transactions(item_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_from_loc ON transactions(from_location_id) WHERE from_location_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_to_loc ON transactions(to_location_id) WHERE to_location_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_type_created ON transactions(tx_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_batch_id ON transactions(batch_id) WHERE batch_id != '';
CREATE INDEX IF NOT EXISTS idx_transactions_ref_tx ON transactions(reference_tx_id) WHERE reference_tx_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_kit_items_kit_id ON kit_items(kit_id);
CREATE INDEX IF NOT EXISTS idx_kit_items_item_id ON kit_items(item_id);

CREATE INDEX IF NOT EXISTS idx_audit_lines_session ON audit_lines(session_id, location_id);
CREATE INDEX IF NOT EXISTS idx_loans_status_due ON loans(status, due_at);
CREATE INDEX IF NOT EXISTS idx_loans_item_id ON loans(item_id, status);
