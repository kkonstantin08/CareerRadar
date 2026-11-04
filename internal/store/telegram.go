package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

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
	if chatID == 0 || len(code) > 128 {
		return errors.New("invalid link")
	}
	digest := sha256.Sum256([]byte(code))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM telegram_tokens WHERE token_hash=$1 AND expires_at>now()`, digest[:])
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("invalid or expired link code")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_link(singleton,chat_id) VALUES(true,$1)`, chatID); err != nil {
		return errors.New("Telegram already linked")
	}
	return tx.Commit()
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
