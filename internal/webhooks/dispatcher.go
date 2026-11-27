package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/version"
)

// SignPayload computes the HMAC-SHA256 signature header value ("sha256=<hex>")
// used in X-Kratel-Signature when a webhook secret is configured.
func SignPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// NotifyLedgerEvent is an EventHookFunc-compatible dispatcher that asynchronously delivers
// transaction.created and low_stock events to subscribed, enabled webhook endpoints.
func (s *Service) NotifyLedgerEvent(tx *models.Transaction, item *models.Item, crossedLowStock bool) {
	if s == nil || s.store == nil || tx == nil {
		return
	}
	recs, err := s.listWebhookRecords(context.Background(), true)
	if err != nil || len(recs) == 0 {
		return
	}

	for _, rec := range recs {
		if hasEventSubscription(rec.Events, EventTransactionCreated) {
			r := rec
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				_ = s.deliverAndRecord(context.Background(), r, EventTransactionCreated, map[string]interface{}{
					"transaction": tx,
					"item":        item,
				})
			}()
		}

		if crossedLowStock && item != nil && hasEventSubscription(rec.Events, EventLowStock) {
			r := rec
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				_ = s.deliverAndRecord(context.Background(), r, EventLowStock, map[string]interface{}{
					"item":        item,
					"transaction": tx,
				})
			}()
		}
	}
}

// SendTestEvent synchronously sends a signed webhook.test ping payload to the specified webhook
// and records its HTTP status and latency.
func (s *Service) SendTestEvent(ctx context.Context, webhookID int64) (*DeliveryResult, error) {
	rec, err := s.getWebhookRecord(ctx, webhookID)
	if err != nil {
		return nil, err
	}
	res := s.deliverAndRecord(ctx, *rec, EventWebhookTest, map[string]interface{}{
		"message": "Kratel webhook test notification",
		"webhook": rec.Name,
	})
	return &res, nil
}

func (s *Service) deliverAndRecord(ctx context.Context, rec webhookRecord, event string, data interface{}) DeliveryResult {
	start := time.Now()
	now := start.UTC().Format(time.RFC3339)

	envelope := WebhookPayload{
		Event:     event,
		Timestamp: now,
		Source:    version.PackageName,
		Version:   version.Get().Version,
		Data:      data,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		res := DeliveryResult{
			WebhookID: rec.ID,
			Event:     event,
			Error:     err.Error(),
		}
		s.updateDeliveryStatus(rec.ID, 0, res.Error)
		return res
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, rec.URL, bytes.NewReader(body))
	if err != nil {
		res := DeliveryResult{
			WebhookID: rec.ID,
			Event:     event,
			Error:     err.Error(),
		}
		s.updateDeliveryStatus(rec.ID, 0, res.Error)
		return res
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.Get().UserAgent())
	req.Header.Set("X-Kratel-Event", event)
	req.Header.Set("X-Kratel-Delivery", fmt.Sprintf("%d-%d", rec.ID, start.UnixNano()))
	if rec.secret != "" {
		req.Header.Set("X-Kratel-Signature", SignPayload(rec.secret, body))
	}

	resp, err := s.client.Do(req)
	durMs := float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		res := DeliveryResult{
			WebhookID:  rec.ID,
			Event:      event,
			DurationMs: durMs,
			Error:      err.Error(),
		}
		s.updateDeliveryStatus(rec.ID, 0, res.Error)
		return res
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	errMsg := ""
	if !success {
		errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	s.updateDeliveryStatus(rec.ID, resp.StatusCode, errMsg)

	return DeliveryResult{
		WebhookID:  rec.ID,
		Event:      event,
		StatusCode: resp.StatusCode,
		Success:    success,
		Error:      errMsg,
		DurationMs: durMs,
	}
}

func (s *Service) updateDeliveryStatus(webhookID int64, status int, errMsg string) {
	if s.store == nil || s.store.DB() == nil {
		return
	}
	_, _ = s.store.DB().Exec(
		`UPDATE webhooks SET last_status = ?, last_error = ? WHERE id = ?;`,
		status, errMsg, webhookID,
	)
}

func hasEventSubscription(events []string, target string) bool {
	for _, ev := range events {
		if ev == target {
			return true
		}
	}
	return false
}
