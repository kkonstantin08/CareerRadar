package store

import (
	"context"
	"database/sql"
	"errors"
)

// QueueNotification stores one notification for a stable event key.
func (s *Store) QueueNotification(ctx context.Context, key, message string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO notifications(event_key,message) VALUES($1,$2) ON CONFLICT(event_key) DO NOTHING`, key, message)
	return err
}

// ClaimNotification marks an event as sending before any external request.
func (s *Store) ClaimNotification(ctx context.Context, key string) (string, error) {
	var message string
	err := s.db.QueryRowContext(ctx, `UPDATE notifications SET state='sending' WHERE event_key=$1 AND state='pending' RETURNING message`, key).Scan(&message)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return message, err
}

// ClaimNextNotification exclusively reserves the oldest pending message.
func (s *Store) ClaimNextNotification(ctx context.Context) (int64, string, error) {
	var id int64
	var message string
	// SKIP LOCKED lets concurrent workers claim different events without waiting.
	err := s.db.QueryRowContext(ctx, `UPDATE notifications SET state='sending' WHERE id=(SELECT id FROM notifications WHERE state='pending' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,message`).Scan(&id, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return id, message, err
}

// FinishNotification records confirmed delivery or an ambiguous outcome.
func (s *Store) FinishNotification(ctx context.Context, id int64, confirmed bool) error {
	state := "unknown"
	if confirmed {
		state = "sent"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET state=$2 WHERE id=$1 AND state='sending'`, id, state)
	return err
}
