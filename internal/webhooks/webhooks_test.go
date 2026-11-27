package webhooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/ledger"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/settings"
)

func TestSettingsAndOperatorPINLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:) failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	svc := settings.NewService(store)

	// 1. Default settings check
	cfg, err := svc.Get(ctx)
	if err != nil {
		t.Fatalf("settings.Get failed: %v", err)
	}
	if cfg.Locale != "en" || cfg.LabelLayout != "thermal_50x25" || cfg.DefaultUnit != "pcs" {
		t.Fatalf("unexpected default settings: %+v", cfg)
	}

	// 2. Update settings to Turkish + TRY + A4 labels + allow negative stock
	cfg.ShopName = "Bosphorus Robotics Lab"
	cfg.Currency = "TRY"
	cfg.Locale = "tr"
	cfg.LabelLayout = "a4_3x8"
	cfg.AllowNegativeStock = true
	updated, err := svc.Update(ctx, *cfg)
	if err != nil {
		t.Fatalf("settings.Update failed: %v", err)
	}
	if updated.ShopName != "Bosphorus Robotics Lab" || updated.Locale != "tr" || !updated.AllowNegativeStock {
		t.Fatalf("unexpected updated settings: %+v", updated)
	}

	// 3. Operator creation & salted PIN verification
	op, err := svc.CreateOperator(ctx, settings.OperatorInput{
		Name: "Barin",
		Role: "Lead Tech",
		PIN:  "4829",
	})
	if err != nil {
		t.Fatalf("CreateOperator failed: %v", err)
	}
	if !op.HasPIN || !op.Active {
		t.Fatalf("expected HasPIN=true, Active=true, got %+v", op)
	}

	if _, err := svc.VerifyOperatorPIN(ctx, op.ID, "4829"); err != nil {
		t.Fatalf("VerifyOperatorPIN valid PIN failed: %v", err)
	}
	if _, err := svc.VerifyOperatorPIN(ctx, op.ID, "0000"); !apperr.HasCode(err, apperr.CodeUnauthorizedPIN) {
		t.Fatalf("expected UNAUTHORIZED_PIN for wrong PIN, got %v", err)
	}
}

func TestWebhookHMACSigningTestEventAndLowStockDispatch(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:) failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	var (
		mu       sync.Mutex
		received []struct {
			event string
			sig   string
			body  []byte
		}
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, struct {
			event string
			sig   string
			body  []byte
		}{
			event: r.Header.Get("X-Kratel-Event"),
			sig:   r.Header.Get("X-Kratel-Signature"),
			body:  body,
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	whSvc := NewService(store)

	// Invalid URL must be rejected
	if _, err := whSvc.CreateWebhook(ctx, WebhookInput{
		Name: "Bad URL",
		URL:  "ftp://example.com/hook",
	}); !apperr.HasCode(err, apperr.CodeValidation) {
		t.Fatalf("expected VALIDATION_ERROR for non-HTTP webhook URL, got %v", err)
	}

	const secret = "kratel-super-secret"
	wh, err := whSvc.CreateWebhook(ctx, WebhookInput{
		Name:   "Telegram Bot Relay",
		URL:    ts.URL,
		Secret: secret,
		Events: []string{EventTransactionCreated, EventLowStock},
	})
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}
	if !wh.HasSecret || !wh.Enabled {
		t.Fatalf("unexpected webhook metadata: %+v", wh)
	}

	// 1. Synchronous test ping
	testRes, err := whSvc.SendTestEvent(ctx, wh.ID)
	if err != nil || !testRes.Success || testRes.StatusCode != 200 {
		t.Fatalf("SendTestEvent failed: res=%+v err=%v", testRes, err)
	}

	// 2. Wire webhook dispatcher to ledger service and trigger a low-stock crossing
	cat := catalog.NewService(store)
	led := ledger.NewService(store, cat)
	led.SetEventHook(whSvc.NotifyLedgerEvent)

	item, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:             "WH-PART-1",
		Name:            "Webhook Alert Part",
		MinStock:        "5",
		OpeningQuantity: "6",
	})
	if err != nil {
		t.Fatalf("CreateItem failed: %v", err)
	}

	if _, err := led.RecordMovement(ctx, ledger.MovementInput{
		ItemID:   item.ID,
		TxType:   ledger.TxOut,
		Quantity: "2", // drops from 6 -> 4 (<= min_stock 5)
		Reason:   "Trigger low-stock webhook",
	}); err != nil {
		t.Fatalf("RecordMovement failed: %v", err)
	}

	whSvc.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 webhook deliveries (webhook.test, transaction.created, low_stock), got %d", len(received))
	}

	seenEvents := make(map[string]bool)
	for _, rec := range received {
		seenEvents[rec.event] = true
		wantSig := SignPayload(secret, rec.body)
		if rec.sig != wantSig {
			t.Errorf("HMAC signature mismatch for event %q: got %q want %q", rec.event, rec.sig, wantSig)
		}
		var env WebhookPayload
		if err := json.Unmarshal(rec.body, &env); err != nil {
			t.Errorf("invalid webhook JSON payload: %v", err)
		}
	}

	for _, want := range []string{EventWebhookTest, EventTransactionCreated, EventLowStock} {
		if !seenEvents[want] {
			t.Errorf("missing expected webhook event %q", want)
		}
	}
}
