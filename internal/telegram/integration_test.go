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
	"testing"
	"time"

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
