// Package store persists sources and observed opportunities in PostgreSQL.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/migrations"
)

// Store holds the shared database connection pool.
type Store struct{ db *sql.DB }

// Source is a user-managed monitoring endpoint.
type Source struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Problem string `json:"problem"`
}

// Observation records facts published at a job URL.
type Observation struct {
	SourceID    int64  `json:"source_id"`
	PageURL     string `json:"page_url"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Company     string `json:"company"`
	Description string `json:"description"`
	Deadline    string `json:"deadline"`
	Location    string `json:"location"`
	WorkMode    string `json:"work_mode"`
	Type        string `json:"type"`
	Level       string `json:"level"`
}

// Job includes a persisted observation and its application status.
type Job struct {
	Observation
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// Open connects to PostgreSQL and creates the initial schema if necessary.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	if err = migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize schema setup between processes while leaving normal queries unlocked.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':careerradar:migrations',0))`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (checksum BYTEA PRIMARY KEY)`); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(migrations.Initial))
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE checksum=$1)`, sum[:]).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err = tx.ExecContext(ctx, migrations.Initial); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(checksum) VALUES($1)`, sum[:]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// AddSource adds a source once, returning the existing source for a duplicate URL.
func (s *Store) AddSource(ctx context.Context, name, url string, enabled bool) (Source, error) {
	var x Source
	err := s.db.QueryRowContext(ctx, `INSERT INTO sources(name,url,enabled) VALUES($1,$2,$3) ON CONFLICT(url) DO UPDATE SET url=EXCLUDED.url RETURNING id,name,url,enabled,problem`, name, url, enabled).Scan(&x.ID, &x.Name, &x.URL, &x.Enabled, &x.Problem)
	return x, err
}

// Sources lists saved sources.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,url,enabled,problem FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	xs := []Source{}
	for rows.Next() {
		var x Source
		if err := rows.Scan(&x.ID, &x.Name, &x.URL, &x.Enabled, &x.Problem); err != nil {
			return nil, err
		}
		xs = append(xs, x)
	}
	return xs, rows.Err()
}

// Observe saves changed facts without overwriting known fields with empty values.
func (s *Store) Observe(ctx context.Context, x Observation) (bool, error) {
	u, err := url.Parse(x.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return false, errors.New("invalid job URL")
	}
	u.Fragment = ""
	query := u.Query()
	for key := range query {
		if strings.HasPrefix(strings.ToLower(key), "utm_") {
			query.Del(key)
		}
	}
	u.RawQuery = query.Encode()
	x.URL = u.String()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// The URL lock serializes first inserts and updates across concurrent checks.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, x.URL); err != nil {
		return false, err
	}
	var id int64
	var old Observation
	err = tx.QueryRowContext(ctx, `SELECT id,title,company,description,deadline,location,work_mode,offer_type,level FROM jobs WHERE url=$1 FOR UPDATE`, x.URL).Scan(&id, &old.Title, &old.Company, &old.Description, &old.Deadline, &old.Location, &old.WorkMode, &old.Type, &old.Level)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	for _, pair := range []struct {
		current  *string
		previous string
	}{{&x.Title, old.Title}, {&x.Company, old.Company}, {&x.Description, old.Description}, {&x.Deadline, old.Deadline}, {&x.Location, old.Location}, {&x.WorkMode, old.WorkMode}, {&x.Type, old.Type}, {&x.Level, old.Level}} {
		if *pair.current == "" {
			*pair.current = pair.previous
		}
	}
	changed := errors.Is(err, sql.ErrNoRows) || x.Title != old.Title || x.Company != old.Company || x.Description != old.Description || x.Deadline != old.Deadline || x.Location != old.Location || x.WorkMode != old.WorkMode || x.Type != old.Type || x.Level != old.Level
	if changed {
		err = tx.QueryRowContext(ctx, `INSERT INTO jobs(url,source_id,title,company,description,deadline,location,work_mode,offer_type,level) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(url) DO UPDATE SET title=EXCLUDED.title,company=EXCLUDED.company,description=EXCLUDED.description,deadline=EXCLUDED.deadline,location=EXCLUDED.location,work_mode=EXCLUDED.work_mode,offer_type=EXCLUDED.offer_type,level=EXCLUDED.level,updated_at=now() RETURNING id`, x.URL, x.SourceID, x.Title, x.Company, x.Description, x.Deadline, x.Location, x.WorkMode, x.Type, x.Level).Scan(&id)
		if err != nil {
			return false, err
		}
		snapshot, err := json.Marshal(x)
		if err != nil {
			return false, err
		}
		var revision int64
		if err = tx.QueryRowContext(ctx, `INSERT INTO job_history(job_id,snapshot) VALUES($1,$2) RETURNING id`, id, snapshot).Scan(&revision); err != nil {
			return false, err
		}
		prefs, err := readPreferences(ctx, tx)
		if err != nil {
			return false, err
		}
		if prefs.Match(jobs.Job{Company: x.Company, Location: x.Location, WorkMode: x.WorkMode, Type: x.Type, Level: x.Level}) != jobs.Unsuitable {
			message := fmt.Sprintf("Вакансия: %s\nКомпания: %s\nДедлайн: %s\n%s", x.Title, x.Company, x.Deadline, x.URL)
			if _, err = tx.ExecContext(ctx, `INSERT INTO notifications(event_key,message) VALUES($1,$2) ON CONFLICT(event_key) DO NOTHING`, fmt.Sprintf("job:%d:revision:%d", id, revision), message); err != nil {
				return false, err
			}
		}
	}
	if x.PageURL == "" {
		if err = tx.QueryRowContext(ctx, `SELECT url FROM sources WHERE id=$1`, x.SourceID).Scan(&x.PageURL); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_evidence(job_url,page_url) VALUES($1,$2) ON CONFLICT(job_url,page_url) DO UPDATE SET last_seen=now()`, x.URL, x.PageURL); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

// RecordCheck records a result without treating source errors as closures.
func (s *Store) RecordCheck(ctx context.Context, sourceID int64, problem string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous, name, url string
	if err = tx.QueryRowContext(ctx, `SELECT problem,name,url FROM sources WHERE id=$1 FOR UPDATE`, sourceID).Scan(&previous, &name, &url); err != nil {
		return err
	}
	var checkID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO checks(source_id,error) VALUES($1,$2) RETURNING id`, sourceID, problem).Scan(&checkID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sources SET problem=$2 WHERE id=$1`, sourceID, problem); err != nil {
		return err
	}
	if problem != "" && problem != previous {
		message := fmt.Sprintf("Требует внимания: %s\n%s\n%s", name, problem, url)
		if _, err = tx.ExecContext(ctx, `INSERT INTO notifications(event_key,message) VALUES($1,$2)`, fmt.Sprintf("check:%d", checkID), message); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Jobs lists stored jobs, newest first.
func (s *Store) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(source_id,0),url,title,company,description,deadline,status,location,work_mode,offer_type,level FROM jobs ORDER BY updated_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	xs := []Job{}
	for rows.Next() {
		var x Job
		if err := rows.Scan(&x.ID, &x.SourceID, &x.URL, &x.Title, &x.Company, &x.Description, &x.Deadline, &x.Status, &x.Location, &x.WorkMode, &x.Type, &x.Level); err != nil {
			return nil, err
		}
		xs = append(xs, x)
	}
	return xs, rows.Err()
}

// UpdateSource changes source settings without modifying collected jobs.
func (s *Store) UpdateSource(ctx context.Context, id int64, name, url string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sources SET name=$2,url=$3,enabled=$4 WHERE id=$1`, id, name, url, enabled)
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

// DeleteSource removes a source while retaining its previously collected jobs.
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sources WHERE id=$1`, id)
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

// SetStatus updates the owner's application progress.
func (s *Store) SetStatus(ctx context.Context, id int64, status string) error {
	switch status {
	case "Не откликался", "Откликнулся", "Тестовое", "Собеседование", "Оффер", "Отказ":
	default:
		return errors.New("invalid status")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=$2 WHERE id=$1`, id, status)
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

// ImportSources atomically adds pending sources while ignoring duplicate URLs.
func (s *Store) ImportSources(ctx context.Context, sources []Source) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, source := range sources {
		if _, err = tx.ExecContext(ctx, `INSERT INTO sources(name,url,enabled) VALUES($1,$2,false) ON CONFLICT(url) DO NOTHING`, source.Name, source.URL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CheckResult is a recorded source check and its outcome.
type CheckResult struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	Problem   string    `json:"problem"`
	CreatedAt time.Time `json:"created_at"`
}

// RecentChecks returns the latest source checks.
func (s *Store) RecentChecks(ctx context.Context) ([]CheckResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,COALESCE(s.name,'Удалённый источник'),c.error,c.created_at FROM checks c LEFT JOIN sources s ON s.id=c.source_id ORDER BY c.created_at DESC,c.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CheckResult{}
	for rows.Next() {
		var x CheckResult
		if err = rows.Scan(&x.ID, &x.Source, &x.Problem, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
