package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func queueTelegramReply(ctx context.Context, tx *sql.Tx, updateID int64, message string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO notifications(event_key,message) VALUES($1,$2) ON CONFLICT(event_key) DO NOTHING`, fmt.Sprintf("telegram-reply:%d", updateID), message)
	return err
}

// NewLinkCode issues a random one-use Telegram linking code valid for ten minutes.
func (s *Store) NewLinkCode(ctx context.Context) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(raw[:])
	digest := sha256.Sum256([]byte(code))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM telegram_tokens`); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_tokens(token_hash,expires_at) VALUES($1,now()+interval '10 minutes')`, digest[:]); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return code, nil
}

// LinkTelegram consumes a valid code without replacing an existing owner.
func (s *Store) LinkTelegram(ctx context.Context, code string, chatID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = linkTelegram(ctx, tx, code, chatID); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrInvalidLink denotes an invalid, expired or already-used linking code.
var ErrInvalidLink = errors.New("invalid, expired or already-used link code")

func linkTelegram(ctx context.Context, tx *sql.Tx, code string, chatID int64) error {
	if chatID == 0 || len(code) > 128 {
		return ErrInvalidLink
	}
	digest := sha256.Sum256([]byte(code))
	result, err := tx.ExecContext(ctx, `DELETE FROM telegram_tokens WHERE token_hash=$1 AND expires_at>now()`, digest[:])
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalidLink
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO telegram_link(singleton,chat_id) VALUES(true,$1) ON CONFLICT DO NOTHING`, chatID)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalidLink
	}
	return nil
}

// ApplyTelegramLink atomically links the owner and records the Telegram update.
func (s *Store) ApplyTelegramLink(ctx context.Context, updateID int64, code string, chatID int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING`, updateID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	if err = linkTelegram(ctx, tx, code, chatID); err != nil {
		return false, err
	}
	if err = queueTelegramReply(ctx, tx, updateID, "Telegram подключён."); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// TelegramChat returns the linked Telegram chat ID.
func (s *Store) TelegramChat(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT chat_id FROM telegram_link WHERE singleton=true`).Scan(&id)
	return id, err
}

// ClaimTelegramUpdate prevents a received update from executing twice after restart.
func (s *Store) ClaimTelegramUpdate(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// TelegramUpdateProcessed reports whether a command completed before acknowledgement.
func (s *Store) TelegramUpdateProcessed(ctx context.Context, id int64) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_updates WHERE update_id=$1)`, id).Scan(&exists)
	return exists, err
}

// ApplyTelegramStatus atomically applies a status change and records its update ID.
func (s *Store) ApplyTelegramStatus(ctx context.Context, updateID, chatID, jobID int64, status string) (bool, error) {
	switch status {
	case "Не откликался", "Откликнулся", "Тестовое", "Собеседование", "Оффер", "Отказ":
	default:
		return false, errors.New("invalid status")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var owner int64
	if err = tx.QueryRowContext(ctx, `SELECT chat_id FROM telegram_link WHERE singleton=true`).Scan(&owner); err != nil {
		return false, err
	}
	if owner != chatID {
		return false, errors.New("unauthorized chat")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING`, updateID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	result, err = tx.ExecContext(ctx, `UPDATE jobs SET status=$2 WHERE id=$1`, jobID, status)
	if err != nil {
		return false, err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, sql.ErrNoRows
	}
	if err = queueTelegramReply(ctx, tx, updateID, "Статус изменён."); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ApplyTelegramSource commits a source mutation and its update acknowledgement together.
func (s *Store) ApplyTelegramSource(ctx context.Context, updateID, chatID int64, action string, sourceID int64, name, sourceURL string) (int64, bool, error) {
	switch action {
	case "add", "edit":
		u, err := url.Parse(sourceURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || strings.TrimSpace(name) == "" {
			return 0, false, errors.New("invalid source")
		}
	case "confirm", "disable", "delete":
	default:
		return 0, false, errors.New("invalid action")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var owner int64
	if err = tx.QueryRowContext(ctx, `SELECT chat_id FROM telegram_link WHERE singleton=true`).Scan(&owner); err != nil {
		return 0, false, err
	}
	if owner != chatID {
		return 0, false, errors.New("unauthorized chat")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING`, updateID)
	if err != nil {
		return 0, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n == 0 {
		return 0, false, tx.Commit()
	}
	switch action {
	case "add":
		source, e := addSource(ctx, tx, strings.TrimSpace(name), sourceURL, false)
		if e != nil {
			return 0, false, e
		}
		sourceID = source.ID
	case "edit":
		err = updateSource(ctx, tx, sourceID, strings.TrimSpace(name), sourceURL, false)
	case "confirm", "disable":
		var sourceName, oldURL string
		if err = tx.QueryRowContext(ctx, `SELECT name,url FROM sources WHERE id=$1 FOR UPDATE`, sourceID).Scan(&sourceName, &oldURL); err == nil {
			err = updateSource(ctx, tx, sourceID, sourceName, oldURL, action == "confirm")
		}
	case "delete":
		err = deleteSource(ctx, tx, sourceID)
	}
	if err != nil {
		return 0, false, err
	}
	reply := ""
	switch action {
	case "add":
		reply = fmt.Sprintf("Источник %d сохранён. Для включения: /confirm %d", sourceID, sourceID)
	case "edit":
		reply = "Источник изменён и выключен до повторного подтверждения."
	case "confirm":
		reply = "Источник включён."
	case "disable":
		reply = "Источник выключен."
	case "delete":
		reply = "Источник удалён; вакансии сохранены."
	}
	if err = queueTelegramReply(ctx, tx, updateID, reply); err != nil {
		return 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	return sourceID, true, nil
}

// ErrTelegramSourceDisabled indicates a source that cannot be checked.
var ErrTelegramSourceDisabled = errors.New("source not enabled")

// QueueTelegramCheck commits a manual check request and update acknowledgement.
func (s *Store) QueueTelegramCheck(ctx context.Context, updateID, chatID, sourceID int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var owner int64
	if err = tx.QueryRowContext(ctx, `SELECT chat_id FROM telegram_link WHERE singleton=true`).Scan(&owner); err != nil {
		return false, err
	}
	if owner != chatID {
		return false, errors.New("unauthorized chat")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING`, updateID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM sources WHERE id=$1`, sourceID).Scan(&enabled); err != nil {
		return false, err
	}
	if !enabled {
		return false, ErrTelegramSourceDisabled
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_checks(update_id,source_id) VALUES($1,$2)`, updateID, sourceID); err != nil {
		return false, err
	}
	if err = queueTelegramReply(ctx, tx, updateID, "Проверка поставлена в очередь."); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ClaimTelegramCheck reserves one pending manual check for a worker.
func (s *Store) ClaimTelegramCheck(ctx context.Context) (int64, int64, int64, error) {
	var updateID, sourceID, attempt int64
	err := s.db.QueryRowContext(ctx, `UPDATE telegram_checks SET state='running',claimed_at=now(),attempt=attempt+1 WHERE update_id=(SELECT update_id FROM telegram_checks WHERE state='pending' ORDER BY update_id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING update_id,source_id,attempt`).Scan(&updateID, &sourceID, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, nil
	}
	return updateID, sourceID, attempt, err
}

// RecoverTelegramChecks makes abandoned checks eligible to run again.
func (s *Store) RecoverTelegramChecks(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_checks SET state='pending' WHERE state='running' AND claimed_at<now()-interval '2 minutes'`)
	return err
}

// FinishTelegramCheck completes only the current worker's attempt.
func (s *Store) FinishTelegramCheck(ctx context.Context, updateID, attempt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_checks SET state='done' WHERE update_id=$1 AND attempt=$2 AND state='running'`, updateID, attempt)
	return err
}

// RecordTelegramReply atomically acknowledges a read-only update and queues its reply.
func (s *Store) RecordTelegramReply(ctx context.Context, updateID, chatID int64, reply string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var owner int64
	if err = tx.QueryRowContext(ctx, `SELECT chat_id FROM telegram_link WHERE singleton=true`).Scan(&owner); err != nil {
		return false, err
	}
	if owner != chatID {
		return false, errors.New("unauthorized chat")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING`, updateID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	if err = queueTelegramReply(ctx, tx, updateID, reply); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// RecordInvalidTelegramLink acknowledges a rejected private link attempt and queues its generic response.
func (s *Store) RecordInvalidTelegramLink(ctx context.Context, updateID, chatID int64) (bool, error) {
	if chatID <= 0 {
		return false, errors.New("invalid chat")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING`, updateID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO notifications(event_key,message,target_chat_id) VALUES($1,$2,$3) ON CONFLICT(event_key) DO NOTHING`, fmt.Sprintf("telegram-reply:%d", updateID), "Код недействителен, истёк или аккаунт уже привязан.", chatID)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
