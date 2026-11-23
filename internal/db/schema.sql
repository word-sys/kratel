-- Kratel (kratel) Core Normalized SQLite Schema
-- Designed for single-file portability, fixed-point Millis accuracy (1000 == 1.000),
-- and append-only ledger integrity.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL
);

-- 1. Lightweight supplier directory
CREATE TABLE IF NOT EXISTS suppliers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT UNIQUE,
    name       TEXT NOT NULL,
    contact    TEXT NOT NULL DEFAULT '',
    email      TEXT NOT NULL DEFAULT '',
    phone      TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- 2. Hierarchical storage bins / shelves / rooms
CREATE TABLE IF NOT EXISTS locations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT UNIQUE NOT NULL,
    name       TEXT NOT NULL,
    parent_id  INTEGER REFERENCES locations(id) ON DELETE RESTRICT,
    notes      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- 3. Catalog of trackable items
CREATE TABLE IF NOT EXISTS items (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    sku                 TEXT UNIQUE NOT NULL,
    barcode             TEXT UNIQUE,
    name                TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    category            TEXT NOT NULL DEFAULT '',
    unit                TEXT NOT NULL DEFAULT 'pcs',
    min_stock           REAL NOT NULL DEFAULT 0,
    min_stock_millis    INTEGER NOT NULL DEFAULT 0,
    unit_cost           REAL NOT NULL DEFAULT 0,
    unit_cost_millis    INTEGER NOT NULL DEFAULT 0,
    supplier_id         INTEGER REFERENCES suppliers(id) ON DELETE SET NULL,
    default_location_id INTEGER REFERENCES locations(id) ON DELETE SET NULL,
    lot_number          TEXT NOT NULL DEFAULT '',
    expiry_date         TEXT NOT NULL DEFAULT '', -- Optional ISO YYYY-MM-DD expiry or calibration date
    attributes_json     TEXT NOT NULL DEFAULT '{}',
    photo_data          TEXT NOT NULL DEFAULT '', -- Optional compressed base64 data URL
    archived            INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

-- 4. Multi-barcode & distributor part number aliases (EAN, UPC, Mouser/DigiKey/LCSC, internal QR)
CREATE TABLE IF NOT EXISTS item_aliases (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    code       TEXT UNIQUE NOT NULL,
    alias_type TEXT NOT NULL DEFAULT 'EAN' CHECK (alias_type IN ('EAN', 'SUPPLIER_PART', 'INTERNAL_QR', 'LEGACY_SKU', 'OTHER')),
    notes      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- 5. Materialized current stock per item per location
CREATE TABLE IF NOT EXISTS stock_levels (
    item_id         INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    location_id     INTEGER NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
    quantity        REAL NOT NULL DEFAULT 0,
    quantity_millis INTEGER NOT NULL DEFAULT 0,
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (item_id, location_id)
);

-- 6. Immutable stock movement ledger (source of truth)
CREATE TABLE IF NOT EXISTS transactions (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id              INTEGER NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
    from_location_id     INTEGER REFERENCES locations(id) ON DELETE RESTRICT,
    to_location_id       INTEGER REFERENCES locations(id) ON DELETE RESTRICT,
    tx_type              TEXT NOT NULL CHECK (tx_type IN ('IN', 'OUT', 'TRANSFER', 'ADJUST')),
    quantity             REAL NOT NULL,
    quantity_millis      INTEGER NOT NULL,
    delta_millis         INTEGER NOT NULL DEFAULT 0,
    balance_after_millis INTEGER NOT NULL DEFAULT 0,
    unit_cost_millis     INTEGER NOT NULL DEFAULT 0,
    lot_number           TEXT NOT NULL DEFAULT '',
    expiry_date          TEXT NOT NULL DEFAULT '',
    batch_id             TEXT NOT NULL DEFAULT '', -- Groups kit/BOM or stocktake movements for batch undo
    reason               TEXT NOT NULL DEFAULT '',
    operator             TEXT NOT NULL DEFAULT '',
    reference_tx_id      INTEGER REFERENCES transactions(id),
    undone               INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL
);

-- 7. BOM / Kits & Repair Bundles
CREATE TABLE IF NOT EXISTS kits (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    code        TEXT UNIQUE NOT NULL,
    barcode     TEXT UNIQUE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    category    TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS kit_items (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    kit_id          INTEGER NOT NULL REFERENCES kits(id) ON DELETE CASCADE,
    item_id         INTEGER NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
    quantity        REAL NOT NULL,
    quantity_millis INTEGER NOT NULL,
    notes           TEXT NOT NULL DEFAULT '',
    UNIQUE (kit_id, item_id)
);

-- 8. Physical Cycle-Count / Stocktake Audit Sessions
CREATE TABLE IF NOT EXISTS audit_sessions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    code         TEXT UNIQUE NOT NULL,
    name         TEXT NOT NULL,
    location_id  INTEGER REFERENCES locations(id) ON DELETE SET NULL,
    status       TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'COMMITTED', 'CANCELLED')),
    operator     TEXT NOT NULL DEFAULT '',
    notes        TEXT NOT NULL DEFAULT '',
    batch_id     TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    committed_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS audit_lines (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      INTEGER NOT NULL REFERENCES audit_sessions(id) ON DELETE CASCADE,
    item_id         INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    location_id     INTEGER NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
    expected_millis INTEGER NOT NULL DEFAULT 0,
    counted_millis  INTEGER NOT NULL DEFAULT 0,
    counted         INTEGER NOT NULL DEFAULT 0,
    notes           TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL,
    UNIQUE (session_id, item_id, location_id)
);

-- 9. Tool & Test-Gear Check-Out / Loaner Tracker
CREATE TABLE IF NOT EXISTS loans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id         INTEGER NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
    location_id     INTEGER NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
    quantity        REAL NOT NULL,
    quantity_millis INTEGER NOT NULL,
    borrower        TEXT NOT NULL,
    operator        TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETURNED')),
    out_tx_id       INTEGER REFERENCES transactions(id),
    in_tx_id        INTEGER REFERENCES transactions(id),
    checked_out_at  TEXT NOT NULL,
    due_at          TEXT NOT NULL DEFAULT '',
    returned_at     TEXT NOT NULL DEFAULT ''
);

-- 10. Shop Preferences, Operators & Outgoing Webhooks
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS operators (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT UNIQUE NOT NULL,
    role       TEXT NOT NULL DEFAULT 'Technician',
    pin_hash   TEXT NOT NULL DEFAULT '',
    active     INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS webhooks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL,
    secret      TEXT NOT NULL DEFAULT '',
    events_json TEXT NOT NULL DEFAULT '["low_stock","transaction.created"]',
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_status INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
