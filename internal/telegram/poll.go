package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Run receives private commands until the context is cancelled.
func (b *Bot) Run(ctx context.Context, token string) {
	b.poll(ctx, &http.Client{Timeout: 35 * time.Second}, "https://api.telegram.org", token)
}

func (b *Bot) poll(ctx context.Context, client *http.Client, base, token string) {
	offset := int64(0)
	for ctx.Err() == nil {
		var response struct {
			OK     bool `json:"ok"`
			Result []struct {
				ID      int64 `json:"update_id"`
				Message struct {
					Chat struct {
						ID   int64  `json:"id"`
						Type string `json:"type"`
					} `json:"chat"`
					From struct {
						ID int64 `json:"id"`
					} `json:"from"`
					Text string `json:"text"`
				} `json:"message"`
			} `json:"result"`
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/bot%s/getUpdates?timeout=25&offset=%d", base, token, offset), nil)
		if err != nil {
			return
		}
		res, err := client.Do(req)
		if err == nil {
			err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&response)
			res.Body.Close()
		}
		if err != nil || !response.OK {
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		retryUpdate := false
		for _, update := range response.Result {
			claims, hasClaims := b.links.(interface {
				ClaimTelegramUpdate(context.Context, int64) (bool, error)
			})
			if lookup, ok := b.links.(interface {
				TelegramUpdateProcessed(context.Context, int64) (bool, error)
			}); ok {
				done, err := lookup.TelegramUpdateProcessed(ctx, update.ID)
				if err != nil {
					retryUpdate = true
					break
				}
				if done {
					offset = update.ID + 1
					continue
				}
			}
			m := update.Message
			message := Message{ChatID: m.Chat.ID, UserID: m.From.ID, ChatType: m.Chat.Type, Text: m.Text}
			atomic, reply, err := b.atomicLink(ctx, update.ID, message)
			if err != nil {
				retryUpdate = true
				break
			}
			if !atomic {
				atomic, reply, err = b.atomicStatus(ctx, update.ID, message)
			}
			if err != nil {
				retryUpdate = true
				break
			}
			if !atomic {
				atomic, reply, err = b.atomicSource(ctx, update.ID, message)
				if err != nil {
					retryUpdate = true
					break
				}
			}
			if !atomic {
				atomic, reply, err = b.atomicCheck(ctx, update.ID, message)
				if err != nil {
					retryUpdate = true
					break
				}
			}
			if !atomic {
				reply, err = b.handle(ctx, message)
				if err != nil {
					retryUpdate = true
					break
				}
			}
			parts := strings.Fields(message.Text)
			linkAttempt := len(parts) == 2 && parts[0] == "/start"
			if !atomic && reply != "" && linkAttempt && message.ChatType == "private" && message.ChatID == message.UserID {
				if queue, ok := b.links.(interface {
					RecordInvalidTelegramLink(context.Context, int64, int64) (bool, error)
				}); ok {
					_, err = queue.RecordInvalidTelegramLink(ctx, update.ID, message.ChatID)
					if err != nil {
						retryUpdate = true
						break
					}
					offset = update.ID + 1
					continue
				}
			}
			if !atomic && reply != "" && !linkAttempt {
				if queue, ok := b.links.(interface {
					RecordTelegramReply(context.Context, int64, int64, string) (bool, error)
				}); ok {
					_, err = queue.RecordTelegramReply(ctx, update.ID, message.ChatID, reply)
					if err != nil {
						retryUpdate = true
						break
					}
					offset = update.ID + 1
					continue
				}
			}
			if hasClaims && !atomic {
				claimed, err := claims.ClaimTelegramUpdate(ctx, update.ID)
				if err != nil {
					retryUpdate = true
					break
				}
				if !claimed {
					offset = update.ID + 1
					continue
				}
			}
			offset = update.ID + 1
			// Atomic commands persist their replies in the notification outbox.
			if atomic {
				continue
			}
			if reply == "" {
				continue
			}
			payload, _ := json.Marshal(map[string]any{"chat_id": m.Chat.ID, "text": reply})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+token+"/sendMessage", bytes.NewReader(payload))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			if res, err := client.Do(req); err == nil {
				io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
				res.Body.Close()
			}
		}
		if retryUpdate {
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

	}
}
