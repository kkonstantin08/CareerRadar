package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// ReserveJobicyPoll enforces Jobicy's shared one-hour automated polling interval.
func (s *Store) ReserveJobicyPoll(ctx context.Context) (bool, error) {
	var reserved bool
	err := s.db.QueryRowContext(ctx, `INSERT INTO jobicy_poll(singleton,polled_at) VALUES(true,now()) ON CONFLICT(singleton) DO UPDATE SET polled_at=EXCLUDED.polled_at WHERE jobicy_poll.polled_at <= now()-interval '1 hour' RETURNING true`).Scan(&reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return reserved, err
}

// ConfirmClosedURL closes an already observed URL and retains the supplied evidence.
func (s *Store) ConfirmClosedURL(ctx context.Context, jobURL, evidence string) (bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE url=$1`, jobURL).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.ConfirmClosed(ctx, id, evidence)
}

// ConfirmClosed records the owner's confirmation and its evidence without inferring closure from errors.
func (s *Store) ConfirmClosed(ctx context.Context, id int64, evidence string) error {
	u, err := url.Parse(evidence)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return errors.New("valid evidence URL required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var jobURL, title string
	var closed bool
	if err = tx.QueryRowContext(ctx, `SELECT url,title,closed FROM jobs WHERE id=$1 FOR UPDATE`, id).Scan(&jobURL, &title, &closed); err != nil {
		return err
	}
	if closed {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET closed=true,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	snapshot, err := json.Marshal(map[string]any{"closed": true, "evidence": evidence, "title": title})
	if err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO job_history(job_id,snapshot) VALUES($1,$2) RETURNING id`, id, snapshot).Scan(&revision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_evidence(job_url,page_url) VALUES($1,$2) ON CONFLICT(job_url,page_url) DO UPDATE SET last_seen=now()`, jobURL, evidence); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO notifications(event_key,message) VALUES($1,$2) ON CONFLICT(event_key) DO NOTHING`, fmt.Sprintf("closure:%d:%d", id, revision), fmt.Sprintf("Закрытие подтверждено: %s\n%s", title, evidence)); err != nil {
		return err
	}
	return tx.Commit()
}

// Reopen lets the owner correct or reverse a closure while retaining history.
func (s *Store) Reopen(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var closed bool
	if err = tx.QueryRowContext(ctx, `SELECT closed FROM jobs WHERE id=$1 FOR UPDATE`, id).Scan(&closed); err != nil {
		return err
	}
	if !closed {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET closed=false,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_history(job_id,snapshot) VALUES($1,'{"closed":false}'::jsonb)`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM notifications WHERE event_key LIKE $1 AND state='pending'`, fmt.Sprintf("closure:%d:%%", id)); err != nil {
		return err
	}
	return tx.Commit()
}
