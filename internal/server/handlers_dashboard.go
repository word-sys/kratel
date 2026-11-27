package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/backup"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/csvmig"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/labels"
	"github.com/word-sys/kratel/internal/ledger"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
	"github.com/word-sys/kratel/internal/settings"
	"github.com/word-sys/kratel/internal/version"
	"github.com/word-sys/kratel/internal/webhooks"
)

// Server wires all Kratel domain services to the REST API v1 and embedded web UI.
type Server struct {
	store     *db.Store
	catalog   *catalog.Service
	ledger    *ledger.Service
	csvmig    *csvmig.Service
	labels    *labels.Service
	backup    *backup.Service
	settings  *settings.Service
	webhooks  *webhooks.Service
	netInfo   NetworkInfo
	startedAt time.Time
}

// Config configures optional network and backup settings for NewServer.
type Config struct {
	BindAddr       string
	Port           int
	TLSEnabled     bool
	TLSFingerprint string
	BackupDir      string
}

// NewServer initializes all domain services and returns a ready-to-mount HTTP Server.
func NewServer(store *db.Store, cfg Config) *Server {
	cat := catalog.NewService(store)
	led := ledger.NewService(store, cat)
	wh := webhooks.NewService(store)
	led.SetEventHook(wh.NotifyLedgerEvent)

	if cfg.Port <= 0 {
		cfg.Port = version.DefaultPort
	}
	if strings.TrimSpace(cfg.BindAddr) == "" {
		cfg.BindAddr = "127.0.0.1"
	}

	return &Server{
		store:     store,
		catalog:   cat,
		ledger:    led,
		csvmig:    csvmig.NewService(store, cat),
		labels:    labels.NewService(store, cat),
		backup:    backup.NewService(store, cfg.BackupDir),
		settings:  settings.NewService(store),
		webhooks:  wh,
		netInfo:   BuildNetworkInfo(cfg.BindAddr, cfg.Port, cfg.TLSEnabled, cfg.TLSFingerprint),
		startedAt: time.Now().UTC(),
	}
}

// Network returns the computed local/LAN address and QR metadata.
func (s *Server) Network() NetworkInfo {
	return s.netInfo
}

// Close flushes pending async webhook deliveries before process exit.
func (s *Server) Close() {
	if s.webhooks != nil {
		s.webhooks.Wait()
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, apperr.Validation("method", "method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"brand":          version.BrandName,
		"package":        version.PackageName,
		"version":        version.Get().Version,
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"fts5_enabled":   s.store.FTSEnabled(),
	})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, apperr.Validation("method", "method not allowed"))
		return
	}
	ctx := r.Context()
	health, err := s.store.CheckHealth(ctx)
	if err != nil {
		writeError(w, err)
		return
	}
	shopCfg, err := s.settings.Get(ctx)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":        version.Get(),
		"db_path":        s.store.Path(),
		"is_memory":      s.store.IsMemory(),
		"backup_dir":     s.backup.BackupDir(),
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"health":         health,
		"network":        s.netInfo,
		"settings":       shopCfg,
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, apperr.Validation("method", "method not allowed"))
		return
	}
	ctx := r.Context()

	shopCfg, err := s.settings.Get(ctx)
	if err != nil {
		writeError(w, err)
		return
	}

	activeItems, _, err := s.catalog.ListItems(ctx, catalog.ItemFilter{IncludeArchived: false})
	if err != nil {
		writeError(w, err)
		return
	}

	var archivedCount int
	_ = s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE archived = 1;`).Scan(&archivedCount)

	var locCount, supCount int
	_ = s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM locations;`).Scan(&locCount)
	_ = s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM suppliers;`).Scan(&supCount)

	todayStart := time.Now().UTC().Format("2006-01-02") + "T00:00:00Z"
	var todayMovements int
	_ = s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE created_at >= ?;`, todayStart).Scan(&todayMovements)

	var totalUnits, totalValuation num.Millis
	for _, it := range activeItems {
		totalUnits = totalUnits.Add(it.TotalStockMillis)
		totalValuation = totalValuation.Add(it.ValuationMillis)
	}

	lowQueue, err := s.catalog.ListLowStockReorderQueue(ctx)
	if err != nil {
		writeError(w, err)
		return
	}
	if lowQueue == nil {
		lowQueue = []models.ReorderItem{}
	}

	expiring, err := s.catalog.ListExpiringItems(ctx, shopCfg.ExpiryWarningDays)
	if err != nil {
		writeError(w, err)
		return
	}
	if expiring == nil {
		expiring = []models.Item{}
	}

	activeLoans, err := s.ledger.ListLoans(ctx, "ACTIVE")
	if err != nil {
		writeError(w, err)
		return
	}
	if activeLoans == nil {
		activeLoans = []models.Loan{}
	}

	recentTxs, _, err := s.ledger.ListTransactions(ctx, ledger.TransactionFilter{Limit: 25})
	if err != nil {
		writeError(w, err)
		return
	}
	if recentTxs == nil {
		recentTxs = []models.Transaction{}
	}

	summary := models.DashboardSummary{
		TotalActiveItems:      len(activeItems),
		TotalArchivedItems:    archivedCount,
		TotalLocations:        locCount,
		TotalSuppliers:        supCount,
		TotalStockUnitsMillis: totalUnits,
		TotalStockUnitsText:   totalUnits.Format(),
		TotalValuationMillis:  totalValuation,
		TotalValuationText:    totalValuation.FormatFixed(2),
		LowStockCount:         len(lowQueue),
		ExpiringSoonCount:     len(expiring),
		ActiveLoanCount:       len(activeLoans),
		TodayMovementCount:    todayMovements,
		LowStockQueue:         lowQueue,
		ExpiringItems:         expiring,
		ActiveLoans:           activeLoans,
		RecentTransactions:    recentTxs,
	}

	writeJSON(w, http.StatusOK, summary)
}
