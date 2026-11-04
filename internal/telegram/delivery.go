package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type deliveryStore interface {
	TelegramChat(context.Context) (int64, error)
	ClaimNextNotification(context.Context) (int64, int64, string, error)
	FinishNotification(context.Context, int64, int64, bool) error
	RecoverStaleNotifications(context.Context) error
}

func send(ctx context.Context, client *http.Client, base, token string, chat int64, message string) bool {
	payload, err := json.Marshal(map[string]any{"chat_id": chat, "text": message})
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var reply struct {
		OK bool `json:"ok"`
	}
	return res.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&reply) == nil && reply.OK
}

// Deliver sends each queued event at most once, retaining uncertain outcomes.
func Deliver(ctx context.Context, db deliveryStore, token string) {
	client := &http.Client{Timeout: 15 * time.Second}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := db.RecoverStaleNotifications(ctx); err != nil {
			continue
		}
		chat, err := db.TelegramChat(ctx)
		if err != nil {
			continue
		}
		id, attempt, message, err := db.ClaimNextNotification(ctx)
		if err != nil || id == 0 {
			continue
		}
		confirmed := send(ctx, client, "https://api.telegram.org", token, chat, message)
		// An interrupted or ambiguous send is never retried automatically.
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = db.FinishNotification(finishCtx, id, attempt, confirmed)
		cancel()
	}
}
