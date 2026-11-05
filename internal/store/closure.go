package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
)

// ErrDuplicateJobicy indicates existing ambiguous records for one listing.
var ErrDuplicateJobicy = errors.New("multiple stored Jobicy URLs for the same listing; source requires attention")

// jobicyStoredURL preserves legacy URLs and refuses ambiguous existing duplicates.
func jobicyStoredURL(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, raw string) (string, error) {
	id := collector.JobicyID(raw)
	if id == "" {
		return raw, nil
	}
	path := "/jobs/" + id + "%"
	// The prefix index bounds candidates; the parser below verifies the exact ID.
	rows, err := db.QueryContext(ctx, `SELECT url FROM jobs WHERE lower(url) LIKE $1 OR lower(url) LIKE $2 OR lower(url) LIKE $3 OR lower(url) LIKE $4`, "https://jobicy.com"+path, "https://www.jobicy.com"+path, "https://jobicy.com:443"+path, "https://www.jobicy.com:443"+path)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	found := ""
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return "", err
		}
		if collector.JobicyID(candidate) != id {
			continue
		}
		if found != "" && found != candidate {
			return "", ErrDuplicateJobicy
		}
		found = candidate
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if found != "" {
		return found, nil
	}
	return collector.JobicyCanonicalURL(raw), nil
}

// ReserveJobicyPoll enforces Jobicy's shared one-hour automated polling interval.
func (s *Store) ReserveJobicyPoll(ctx context.Context) (bool, error) {
	var reserved bool
	err := s.db.QueryRowContext(ctx, `INSERT INTO jobicy_poll(singleton,polled_at) VALUES(true,now()) ON CONFLICT(singleton) DO UPDATE SET polled_at=EXCLUDED.polled_at WHERE jobicy_poll.polled_at <= now()-interval '1 hour' RETURNING true`).Scan(&reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return reserved, err
}

// JobicyStatus returns a recently checked status, if one exists.
func (s *Store) JobicyStatus(ctx context.Context, id string) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM jobicy_status WHERE job_id=$1 AND checked_at > now()-interval '1 hour'`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return status, err
}

// JobicyChecked returns the source result recorded since its cached status was fetched.
func (s *Store) JobicyChecked(ctx context.Context, sourceID int64, id string) (string, bool, error) {
	var problem string
	err := s.db.QueryRowContext(ctx, `SELECT c.error FROM checks c JOIN jobicy_status j ON j.job_id=$2 WHERE c.source_id=$1 AND c.created_at >= j.checked_at AND j.checked_at > now()-interval '1 hour' ORDER BY c.id DESC LIMIT 1`, sourceID, id).Scan(&problem)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return problem, err == nil, err
}

// SaveJobicyStatuses stores one completed synchronization pass atomically.
func (s *Store) SaveJobicyStatuses(ctx context.Context, statuses map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, status := range statuses {
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobicy_status(job_id,status,checked_at) VALUES($1,$2,now()) ON CONFLICT(job_id) DO UPDATE SET status=EXCLUDED.status,checked_at=EXCLUDED.checked_at`, id, status); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ConfirmClosedURL closes an already observed URL and retains the supplied evidence.
func (s *Store) ConfirmClosedURL(ctx context.Context, jobURL, evidence string) (bool, error) {
	if err := validateClosureEvidence(jobURL, evidence); err != nil {
		return false, err
	}
	if canonical := collector.GreenhouseCanonicalURL(jobURL); canonical != "" {
		jobURL = canonical
	}
	jobURL, err := jobicyStoredURL(ctx, s.db, jobURL)
	if err != nil {
		return false, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE url=$1`, jobURL).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	origin := "jobicy"
	if collector.GreenhouseClosureEvidenceURL(jobURL) == evidence {
		origin = "greenhouse"
	}
	return true, s.confirmClosed(ctx, id, evidence, origin)
}

// ConfirmClosed records the owner's confirmation and its evidence without inferring closure from errors.
func (s *Store) ConfirmClosed(ctx context.Context, id int64, evidence string) error {
	return s.confirmClosed(ctx, id, evidence, "owner")
}

func (s *Store) confirmClosed(ctx context.Context, id int64, evidence, origin string) error {
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
	var oldOrigin string
	if err = tx.QueryRowContext(ctx, `SELECT url,title,closed,closed_by FROM jobs WHERE id=$1 FOR UPDATE`, id).Scan(&jobURL, &title, &closed, &oldOrigin); err != nil {
		return err
	}
	if closed {
		if origin == "owner" {
			if _, err = tx.ExecContext(ctx, `UPDATE jobs SET closed_by='owner' WHERE id=$1`, id); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO job_evidence(job_url,page_url) VALUES($1,$2) ON CONFLICT(job_url,page_url) DO NOTHING`, jobURL, evidence)
			if err != nil {
				return err
			}
			added, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if oldOrigin != "owner" || added != 0 {
				snapshot, err := json.Marshal(map[string]any{"closed": true, "evidence": evidence, "confirmed_by": "owner", "title": title})
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO job_history(job_id,snapshot) VALUES($1,$2)`, id, snapshot); err != nil {
					return err
				}
			}
		}
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET closed=true,closed_by=$2,updated_at=now() WHERE id=$1`, id, origin); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM notifications WHERE event_key LIKE $1 AND state='pending'`, fmt.Sprintf("reopen:%d:%%", id)); err != nil {
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

// ReopenJobicy reverses only a closure previously established by Jobicy.
func (s *Store) ReopenJobicy(ctx context.Context, jobURL, evidence string) error {
	if err := validateJobicyEvidence(jobURL, evidence); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	jobURL, err = jobicyStoredURL(ctx, tx, jobURL)
	if err != nil {
		return err
	}
	var jobID int64
	var title string
	var closed bool
	var origin string
	err = tx.QueryRowContext(ctx, `SELECT id,title,closed,closed_by FROM jobs WHERE url=$1 FOR UPDATE`, jobURL).Scan(&jobID, &title, &closed, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !closed || origin != "jobicy" {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET closed=false,closed_by='',updated_at=now() WHERE id=$1`, jobID); err != nil {
		return err
	}
	snapshot, err := json.Marshal(map[string]any{"closed": false, "evidence": evidence, "title": title})
	if err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO job_history(job_id,snapshot) VALUES($1,$2) RETURNING id`, jobID, snapshot).Scan(&revision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_evidence(job_url,page_url) VALUES($1,$2) ON CONFLICT(job_url,page_url) DO UPDATE SET last_seen=now()`, jobURL, evidence); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM notifications WHERE event_key LIKE $1 AND state='pending'`, fmt.Sprintf("closure:%d:%%", jobID)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO notifications(event_key,message) VALUES($1,$2) ON CONFLICT(event_key) DO NOTHING`, fmt.Sprintf("reopen:%d:%d", jobID, revision), fmt.Sprintf("Вакансия вновь открыта по данным Jobicy: %s\n%s", title, jobURL)); err != nil {
		return err
	}
	return tx.Commit()
}

func validateJobicyEvidence(jobURL, evidence string) error {
	u, err := url.Parse(evidence)
	id := collector.JobicyID(jobURL)
	if err != nil || id == "" || u.Scheme != "https" || u.Host != "jobicy.com" || u.User != nil || u.Path != "/api/jobs/status" || u.RawQuery != "ids="+id || u.Fragment != "" {
		return errors.New("invalid Jobicy evidence")
	}
	return nil
}

func validateClosureEvidence(jobURL, evidence string) error {
	greenhouseEvidence := collector.GreenhouseClosureEvidenceURL(jobURL)
	if validateJobicyEvidence(jobURL, evidence) == nil || greenhouseEvidence != "" && greenhouseEvidence == evidence {
		return nil
	}
	return errors.New("invalid source closure evidence")
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
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET closed=false,closed_by='',updated_at=now() WHERE id=$1`, id); err != nil {
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
