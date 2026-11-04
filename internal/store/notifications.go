package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// QueueNotification stores one notification for a stable event key.
func (s *Store) QueueNotification(ctx context.Context, key, message string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO notifications(event_key,message) VALUES($1,$2) ON CONFLICT(event_key) DO NOTHING`, key, message)
	return err
}

// ClaimNotification marks an event as sending before any external request.
func (s *Store) ClaimNotification(ctx context.Context, key string) (string, error) {
	var message string
	err := s.db.QueryRowContext(ctx, `UPDATE notifications SET state='sending',claimed_at=now(),attempt=attempt+1 WHERE event_key=$1 AND state='pending' RETURNING message`, key).Scan(&message)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return message, err
}

// ClaimNextNotification exclusively reserves the oldest pending message.
func (s *Store) ClaimNextNotification(ctx context.Context) (int64, int64, string, error) {
	var id, attempt int64
	var message string
	// SKIP LOCKED lets concurrent workers claim different events without waiting.
	err := s.db.QueryRowContext(ctx, `UPDATE notifications SET state='sending',claimed_at=now(),attempt=attempt+1 WHERE id=(SELECT id FROM notifications WHERE state='pending' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,attempt,message`).Scan(&id, &attempt, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, "", nil
	}
	return id, attempt, message, err
}

// FinishNotification records confirmed delivery or an ambiguous outcome.
func (s *Store) FinishNotification(ctx context.Context, id, attempt int64, confirmed bool) error {
	state := "unknown"
	if confirmed {
		state = "sent"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET state=$2 WHERE id=$1 AND state='sending' AND attempt=$3`, id, state, attempt)
	return err
}

// UncertainNotification is a delivery whose outcome requires owner review.
type UncertainNotification struct {
	ID        int64     `json:"id"`
	Message   string    `json:"message"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// UncertainNotifications lists ambiguous sends; in-flight sends are not eligible for resolution.
func (s *Store) UncertainNotifications(ctx context.Context) ([]UncertainNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,message,state,created_at FROM notifications WHERE state='unknown' ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UncertainNotification{}
	for rows.Next() {
		var x UncertainNotification
		if err := rows.Scan(&x.ID, &x.Message, &x.State, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ResolveNotification records the owner's decision; retry is always explicit.
func (s *Store) ResolveNotification(ctx context.Context, id int64, decision string) error {
	state := ""
	switch decision {
	case "sent":
		state = "sent"
	case "retry":
		state = "pending"
	default:
		return errors.New("invalid decision")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE notifications SET state=$2 WHERE id=$1 AND state='unknown'`, id, state)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RecoverStaleNotifications exposes abandoned sends without retrying them.
func (s *Store) RecoverStaleNotifications(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET state='unknown' WHERE state='sending' AND (claimed_at IS NULL OR claimed_at<now()-interval '2 minutes')`)
	return err
}
