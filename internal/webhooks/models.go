package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/db"
)

const (
	EventTransactionCreated = "transaction.created"
	EventLowStock           = "low_stock"
	EventWebhookTest        = "webhook.test"
)

// Webhook represents a registered outgoing HTTP webhook endpoint.
type Webhook struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	HasSecret  bool     `json:"has_secret"`
	Events     []string `json:"events"`
	Enabled    bool     `json:"enabled"`
	LastStatus int      `json:"last_status"`
	LastError  string   `json:"last_error"`
	CreatedAt  string   `json:"created_at"`
}

type webhookRecord struct {
	Webhook
	secret string
}

// WebhookInput carries fields to create or update an outgoing webhook.
type WebhookInput struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled,omitempty"`
}

// WebhookPayload is the JSON envelope delivered to registered HTTP webhook endpoints.
type WebhookPayload struct {
	Event     string      `json:"event"`
	Timestamp string      `json:"timestamp"`
	Source    string      `json:"source"`
	Version   string      `json:"version"`
	Data      interface{} `json:"data"`
}

// DeliveryResult summarizes the HTTP outcome of a webhook delivery attempt.
type DeliveryResult struct {
	WebhookID  int64   `json:"webhook_id"`
	Event      string  `json:"event"`
	StatusCode int     `json:"status_code"`
	Success    bool    `json:"success"`
	Error      string  `json:"error,omitempty"`
	DurationMs float64 `json:"duration_ms"`
}

// Service manages outgoing webhook registrations and dispatches signed event notifications.
type Service struct {
	store  *db.Store
	client *http.Client
	wg     sync.WaitGroup
}

// NewService constructs a webhook Service backed by store.
func NewService(store *db.Store) *Service {
	return &Service{
		store: store,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Wait blocks until all in-flight asynchronous webhook deliveries finish (used during shutdown and tests).
func (s *Service) Wait() {
	s.wg.Wait()
}

// CreateWebhook validates and stores a new outgoing webhook subscription.
func (s *Service) CreateWebhook(ctx context.Context, in WebhookInput) (*Webhook, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "webhook name is required")
	}
	targetURL, err := validateWebhookURL(in.URL)
	if err != nil {
		return nil, err
	}
	events := normalizeEvents(in.Events)
	eventsJSON, _ := json.Marshal(events)

	enabledInt := 1
	if in.Enabled != nil && !*in.Enabled {
		enabledInt = 0
	}
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := s.store.DB().ExecContext(
		ctx,
		`INSERT INTO webhooks (name, url, secret, events_json, enabled, last_status, last_error, created_at)
		 VALUES (?, ?, ?, ?, ?, 0, '', ?);`,
		name, targetURL, strings.TrimSpace(in.Secret), string(eventsJSON), enabledInt, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert webhook: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetWebhookByID(ctx, id)
}

// GetWebhookByID retrieves a Webhook by ID (without exposing its secret).
func (s *Service) GetWebhookByID(ctx context.Context, id int64) (*Webhook, error) {
	rec, err := s.getWebhookRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	wh := rec.Webhook
	return &wh, nil
}

// ListWebhooks returns all registered outgoing webhooks.
func (s *Service) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	recs, err := s.listWebhookRecords(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]Webhook, len(recs))
	for i := range recs {
		out[i] = recs[i].Webhook
	}
	return out, nil
}

// DeleteWebhook deletes an outgoing webhook subscription by ID.
func (s *Service) DeleteWebhook(ctx context.Context, id int64) error {
	res, err := s.store.DB().ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?;`, id)
	if err != nil {
		return fmt.Errorf("delete webhook %d: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return apperr.NotFound("webhook", fmt.Sprintf("%d", id))
	}
	return nil
}

func (s *Service) getWebhookRecord(ctx context.Context, id int64) (*webhookRecord, error) {
	var (
		rec        webhookRecord
		eventsJSON string
		enabledInt int
	)
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT id, name, url, secret, events_json, enabled, last_status, last_error, created_at
		 FROM webhooks WHERE id = ?;`,
		id,
	).Scan(
		&rec.ID, &rec.Name, &rec.URL, &rec.secret, &eventsJSON,
		&enabledInt, &rec.LastStatus, &rec.LastError, &rec.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("webhook", fmt.Sprintf("%d", id))
	}
	if err != nil {
		return nil, err
	}
	rec.HasSecret = rec.secret != ""
	rec.Enabled = enabledInt != 0
	_ = json.Unmarshal([]byte(eventsJSON), &rec.Events)
	if len(rec.Events) == 0 {
		rec.Events = []string{EventLowStock, EventTransactionCreated}
	}
	return &rec, nil
}

func (s *Service) listWebhookRecords(ctx context.Context, enabledOnly bool) ([]webhookRecord, error) {
	q := `SELECT id, name, url, secret, events_json, enabled, last_status, last_error, created_at FROM webhooks`
	if enabledOnly {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY id ASC;`

	rows, err := s.store.DB().QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query webhooks: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []webhookRecord
	for rows.Next() {
		var (
			rec        webhookRecord
			eventsJSON string
			enabledInt int
		)
		if err := rows.Scan(
			&rec.ID, &rec.Name, &rec.URL, &rec.secret, &eventsJSON,
			&enabledInt, &rec.LastStatus, &rec.LastError, &rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		rec.HasSecret = rec.secret != ""
		rec.Enabled = enabledInt != 0
		_ = json.Unmarshal([]byte(eventsJSON), &rec.Events)
		out = append(out, rec)
	}
	return out, nil
}

func validateWebhookURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", apperr.Validation("url", "webhook URL is required")
	}
	u, err := url.ParseRequestURI(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", apperr.Validation("url", "webhook URL must be a valid http:// or https:// endpoint")
	}
	return trimmed, nil
}

func normalizeEvents(events []string) []string {
	if len(events) == 0 {
		return []string{EventLowStock, EventTransactionCreated}
	}
	seen := make(map[string]bool, len(events))
	out := make([]string, 0, len(events))
	for _, ev := range events {
		clean := strings.ToLower(strings.TrimSpace(ev))
		if clean == EventLowStock || clean == EventTransactionCreated {
			if !seen[clean] {
				seen[clean] = true
				out = append(out, clean)
			}
		}
	}
	if len(out) == 0 {
		return []string{EventLowStock, EventTransactionCreated}
	}
	return out
}
