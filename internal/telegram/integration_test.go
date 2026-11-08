package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kkonstantin08/CareerRadar/internal/discovery"
	"github.com/kkonstantin08/CareerRadar/internal/store"
)

func TestPollPersistsLinkAndReplyAcrossRestart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("tgpoll_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	code, err := db.NewLinkCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	calls := 0
	direct := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botTEST/sendMessage" {
			direct = true
			t.Error("atomic reply sent outside outbox")
			return
		}
		calls++
		if calls > 1 {
			cancel()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{map[string]any{"update_id": 42, "message": map[string]any{"chat": map[string]any{"id": 123, "type": "private"}, "from": map[string]any{"id": 123}, "text": "/start " + code}}}})
	}))
	defer srv.Close()
	New(db).poll(pollCtx, srv.Client(), srv.URL, "TEST")
	owner, err := db.TelegramChat(ctx)
	if err != nil || owner != 123 || direct {
		t.Fatalf("owner %d direct %t %v", owner, direct, err)
	}
	db.Close()
	db, err = store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	processed, err := db.TelegramUpdateProcessed(ctx, 42)
	if err != nil || !processed {
		t.Fatalf("update %t %v", processed, err)
	}
	check, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var message, state string
	if err = check.QueryRowContext(ctx, `SELECT message,state FROM notifications WHERE event_key='telegram-reply:42'`).Scan(&message, &state); err != nil || message != "Telegram подключён." || state != "pending" {
		t.Fatalf("reply %q %q %v", message, state, err)
	}
}

func TestSourceWarningDeliveredOnceThroughOutbox(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("tgwarning_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	code, err := db.NewLinkCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.LinkTelegram(ctx, code, 123); err != nil {
		t.Fatal(err)
	}
	source, err := db.AddSource(ctx, "Example", "https://example.org/careers", true)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = db.RecordCheck(ctx, source.ID, "job page has no extractable vacancy; source requires attention"); err != nil {
			t.Fatal(err)
		}
	}
	received := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTEST/sendMessage" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		var payload struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.ChatID != 123 || !strings.Contains(payload.Text, "Требует внимания") || !strings.Contains(payload.Text, source.URL) {
			t.Errorf("payload=%+v", payload)
		}
		received++
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	id, attempt, chat, message, err := db.ClaimNextNotification(ctx)
	if err != nil || id == 0 {
		t.Fatalf("claim id=%d err=%v", id, err)
	}
	if !send(ctx, srv.Client(), srv.URL, "TEST", chat, message) {
		t.Fatal("delivery failed")
	}
	if err = db.FinishNotification(ctx, id, attempt, true); err != nil {
		t.Fatal(err)
	}
	id, _, _, _, err = db.ClaimNextNotification(ctx)
	if err != nil || id != 0 || received != 1 {
		t.Fatalf("next id=%d received=%d err=%v", id, received, err)
	}
}

func TestCompanySearchCommandQueuesReplyWithoutAddingSource(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("tgfind_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	code, err := db.NewLinkCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.LinkTelegram(ctx, code, 123); err != nil {
		t.Fatal(err)
	}
	bot := New(db)
	bot.findCompany = func(context.Context, string) ([]discovery.CompanyResult, error) {
		return []discovery.CompanyResult{{Name: "Example", Homepage: "https://example.org", CareerURLs: []string{"https://example.org/careers"}}}, nil
	}
	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTEST/getUpdates" {
			t.Errorf("unexpected direct send: %s", r.URL.Path)
			return
		}
		calls++
		if calls > 1 {
			cancel()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{map[string]any{"update_id": 42, "message": map[string]any{"chat": map[string]any{"id": 123, "type": "private"}, "from": map[string]any{"id": 123}, "text": "/find Example"}}}})
	}))
	defer srv.Close()
	bot.poll(pollCtx, srv.Client(), srv.URL, "TEST")
	check, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var message, state string
	if err = check.QueryRowContext(ctx, `SELECT message,state FROM notifications WHERE event_key='telegram-reply:42'`).Scan(&message, &state); err != nil || state != "pending" || !strings.Contains(message, "https://example.org/careers") {
		t.Fatalf("reply=%q state=%q error=%v", message, state, err)
	}
	sources, err := db.Sources(ctx)
	if err != nil || len(sources) != 0 {
		t.Fatalf("sources=%+v error=%v", sources, err)
	}
}
