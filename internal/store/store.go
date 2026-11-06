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
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kkonstantin08/CareerRadar/internal/collector"
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
	SourceID          int64  `json:"source_id"`
	PageURL           string `json:"page_url"`
	URL               string `json:"url"`
	ApplyURL          string `json:"apply_url"`
	Title             string `json:"title"`
	Company           string `json:"company"`
	Description       string `json:"description"`
	Deadline          string `json:"deadline"`
	Location          string `json:"location"`
	LocationUncertain bool   `json:"-"`
	WorkMode          string `json:"work_mode"`
	Type              string `json:"type"`
	Level             string `json:"level"`
	Direction         string `json:"direction"`
	Language          string `json:"language"`
	Hours             *int   `json:"hours"`
	Salary            *int64 `json:"salary"`
	SalaryMin         *int64 `json:"salary_min"`
	SalaryMax         *int64 `json:"salary_max"`
	Currency          string `json:"currency"`
}

// Job includes a persisted observation and its application status.
type Job struct {
	Observation
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Closed bool   `json:"closed"`
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
	for _, migration := range []string{migrations.Initial, migrations.CatalogFilters} {
		sum := sha256.Sum256([]byte(migration))
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE checksum=$1)`, sum[:]).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err = tx.ExecContext(ctx, migration); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(checksum) VALUES($1)`, sum[:]); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// AddSource adds a source once, returning the existing source for a duplicate URL.
func (s *Store) AddSource(ctx context.Context, name, url string, enabled bool) (Source, error) {
	return addSource(ctx, s.db, name, url, enabled)
}

func addSource(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name, url string, enabled bool) (Source, error) {
	var x Source
	err := db.QueryRowContext(ctx, `INSERT INTO sources(name,url,enabled) VALUES($1,$2,$3) ON CONFLICT(url) DO UPDATE SET url=EXCLUDED.url RETURNING id,name,url,enabled,problem`, name, url, enabled).Scan(&x.ID, &x.Name, &x.URL, &x.Enabled, &x.Problem)
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
	if x.ApplyURL != "" {
		apply, err := url.Parse(x.ApplyURL)
		if err != nil || (apply.Scheme != "http" && apply.Scheme != "https") || apply.Hostname() == "" || apply.User != nil {
			return false, errors.New("invalid application URL")
		}
	}
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
	if canonical := collector.JobicyCanonicalURL(x.URL); canonical != "" {
		x.URL = canonical
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if id := collector.JobicyID(x.URL); id != "" {
		// Serialize aliases of one listing before choosing an existing URL.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "jobicy:"+id); err != nil {
			return false, err
		}
		x.URL, err = jobicyStoredURL(ctx, tx, x.URL)
		if err != nil {
			return false, err
		}
	}
	// The URL lock serializes first inserts and updates across concurrent checks.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, x.URL); err != nil {
		return false, err
	}
	var id int64
	var old Observation
	err = tx.QueryRowContext(ctx, `SELECT id,title,company,description,deadline,location,work_mode,offer_type,level,weekly_hours,monthly_salary,monthly_salary_min,monthly_salary_max,salary_currency,direction,language,apply_url FROM jobs WHERE url=$1 FOR UPDATE`, x.URL).Scan(&id, &old.Title, &old.Company, &old.Description, &old.Deadline, &old.Location, &old.WorkMode, &old.Type, &old.Level, &old.Hours, &old.Salary, &old.SalaryMin, &old.SalaryMax, &old.Currency, &old.Direction, &old.Language, &old.ApplyURL)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// A changed title can invalidate a title-derived level; explicit internships remain known.
	if x.Level == "" && (x.Title == "" || x.Title == old.Title || old.Level == "Intern" && old.Type == "Internship" && (x.Type == "" || x.Type == "Internship")) {
		x.Level = old.Level
	}
	if x.Location == "" && !x.LocationUncertain && !(x.WorkMode == "remote" && old.WorkMode != "" && old.WorkMode != "remote") {
		x.Location = old.Location
	}
	for _, pair := range []struct {
		current  *string
		previous string
	}{{&x.Title, old.Title}, {&x.Company, old.Company}, {&x.Description, old.Description}, {&x.Deadline, old.Deadline}, {&x.ApplyURL, old.ApplyURL}, {&x.WorkMode, old.WorkMode}, {&x.Type, old.Type}, {&x.Direction, old.Direction}, {&x.Language, old.Language}} {
		if *pair.current == "" {
			*pair.current = pair.previous
		}
	}
	if x.Hours == nil {
		x.Hours = old.Hours
	}
	if x.Salary == nil && x.SalaryMin == nil && x.SalaryMax == nil {
		x.Salary = old.Salary
		x.SalaryMin, x.SalaryMax = old.SalaryMin, old.SalaryMax
		x.Currency = old.Currency
	}
	if (x.Salary != nil || x.SalaryMin != nil || x.SalaryMax != nil) && x.Currency == "" {
		return false, errors.New("salary currency required")
	}
	if x.Salary != nil && (x.SalaryMin != nil || x.SalaryMax != nil) || x.SalaryMin != nil && x.SalaryMax != nil && *x.SalaryMin > *x.SalaryMax || x.Salary != nil && *x.Salary < 0 || x.SalaryMin != nil && *x.SalaryMin < 0 || x.SalaryMax != nil && *x.SalaryMax < 0 {
		return false, errors.New("invalid salary range")
	}
	newJob := errors.Is(err, sql.ErrNoRows)
	changed := !reflect.DeepEqual(x.Hours, old.Hours) || !reflect.DeepEqual(x.Salary, old.Salary) || !reflect.DeepEqual(x.SalaryMin, old.SalaryMin) || !reflect.DeepEqual(x.SalaryMax, old.SalaryMax) || x.Currency != old.Currency || newJob || x.Title != old.Title || x.Company != old.Company || x.Description != old.Description || x.Deadline != old.Deadline || x.ApplyURL != old.ApplyURL || x.Location != old.Location || x.WorkMode != old.WorkMode || x.Type != old.Type || x.Level != old.Level || x.Direction != old.Direction || x.Language != old.Language
	if changed {
		err = tx.QueryRowContext(ctx, `INSERT INTO jobs(url,source_id,title,company,description,deadline,location,work_mode,offer_type,level,weekly_hours,monthly_salary,salary_currency,direction,language,monthly_salary_min,monthly_salary_max,apply_url) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) ON CONFLICT(url) DO UPDATE SET title=EXCLUDED.title,company=EXCLUDED.company,description=EXCLUDED.description,deadline=EXCLUDED.deadline,location=EXCLUDED.location,work_mode=EXCLUDED.work_mode,offer_type=EXCLUDED.offer_type,level=EXCLUDED.level,weekly_hours=EXCLUDED.weekly_hours,monthly_salary=EXCLUDED.monthly_salary,salary_currency=EXCLUDED.salary_currency,direction=EXCLUDED.direction,language=EXCLUDED.language,monthly_salary_min=EXCLUDED.monthly_salary_min,monthly_salary_max=EXCLUDED.monthly_salary_max,apply_url=EXCLUDED.apply_url,updated_at=now() RETURNING id`, x.URL, x.SourceID, x.Title, x.Company, x.Description, x.Deadline, x.Location, x.WorkMode, x.Type, x.Level, x.Hours, x.Salary, x.Currency, x.Direction, x.Language, x.SalaryMin, x.SalaryMax, x.ApplyURL).Scan(&id)
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
		match := prefs.Match(jobs.Job{Company: x.Company, Location: x.Location, WorkMode: x.WorkMode, Type: x.Type, Level: x.Level, Direction: x.Direction, Language: x.Language, Hours: x.Hours, Salary: x.Salary, SalaryMin: x.SalaryMin, SalaryMax: x.SalaryMax, Currency: x.Currency})
		if match != jobs.Unsuitable {
			message := jobNotification(x, old, newJob, match == jobs.NeedsClarification)
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

func jobNotification(current, previous Observation, isNew, needsClarification bool) string {
	short := func(s string) string {
		runes := []rune(strings.TrimSpace(s))
		if len(runes) > 80 {
			return string(runes[:80]) + "…"
		}
		if len(runes) == 0 {
			return "не указано"
		}
		return string(runes)
	}
	var lines []string
	hours := func(n *int) string {
		if n == nil {
			return "не указано"
		}
		return fmt.Sprintf("%d ч/нед.", *n)
	}
	pay := func(j Observation) string {
		if j.Salary != nil {
			return fmt.Sprintf("%d %s/мес.", *j.Salary, short(j.Currency))
		}
		if j.SalaryMin != nil && j.SalaryMax != nil {
			return fmt.Sprintf("%d–%d %s/мес.", *j.SalaryMin, *j.SalaryMax, short(j.Currency))
		}
		if j.SalaryMin != nil {
			return fmt.Sprintf("от %d %s/мес.", *j.SalaryMin, short(j.Currency))
		}
		if j.SalaryMax != nil {
			return fmt.Sprintf("до %d %s/мес.", *j.SalaryMax, short(j.Currency))
		}
		return "не указано"
	}
	if isNew {
		lines = append(lines, "Новая вакансия: "+short(current.Title))
	} else {
		lines = append(lines, "Изменена вакансия: "+short(current.Title))
	}
	lines = append(lines, "Компания: "+short(current.Company))
	if needsClarification {
		lines = append(lines, "Требует уточнения")
	}
	if isNew {
		lines = append(lines, "Дедлайн: "+short(current.Deadline))
		for _, field := range []struct{ name, value string }{
			{"География", current.Location}, {"Формат", current.WorkMode}, {"Уровень", current.Level}, {"Направление", current.Direction}, {"Языки", current.Language}, {"Тип", current.Type},
		} {
			if strings.TrimSpace(field.value) != "" {
				lines = append(lines, field.name+": "+short(field.value))
			}
		}
		if current.Hours != nil {
			lines = append(lines, "Нагрузка: "+hours(current.Hours))
		}
		if current.Salary != nil || current.SalaryMin != nil || current.SalaryMax != nil {
			lines = append(lines, "Оплата: "+pay(current))
		}
	} else {
		for _, field := range []struct{ name, old, current string }{
			{"Название", previous.Title, current.Title}, {"Компания", previous.Company, current.Company}, {"Дедлайн", previous.Deadline, current.Deadline}, {"География", previous.Location, current.Location}, {"Формат", previous.WorkMode, current.WorkMode}, {"Уровень", previous.Level, current.Level}, {"Направление", previous.Direction, current.Direction}, {"Языки", previous.Language, current.Language}, {"Тип", previous.Type, current.Type},
		} {
			if field.old != field.current {
				lines = append(lines, field.name+": "+short(field.old)+" → "+short(field.current))
			}
		}
		if !reflect.DeepEqual(current.Hours, previous.Hours) {
			lines = append(lines, "Нагрузка: "+hours(previous.Hours)+" → "+hours(current.Hours))
		}
		if !reflect.DeepEqual(current.Salary, previous.Salary) || !reflect.DeepEqual(current.SalaryMin, previous.SalaryMin) || !reflect.DeepEqual(current.SalaryMax, previous.SalaryMax) || current.Currency != previous.Currency {
			lines = append(lines, "Оплата: "+pay(previous)+" → "+pay(current))
		}
		if current.Description != previous.Description {
			lines = append(lines, "Изменилось описание")
		}
		if current.ApplyURL != previous.ApplyURL {
			lines = append(lines, "Изменилась ссылка на отклик")
		}
	}
	lines = append(lines, current.URL)
	if current.ApplyURL != "" {
		lines = append(lines, "Отклик: "+current.ApplyURL)
	}
	return strings.Join(lines, "\n")
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

// RecentJobCounts reports distinct new and changed jobs since the given instant.
func (s *Store) RecentJobCounts(ctx context.Context, since time.Time) (total, added, changed int, err error) {
	// The first recorded revision distinguishes newly discovered jobs from updates.
	err = s.db.QueryRowContext(ctx, `WITH recent AS (
		SELECT job_id, MIN(id) AS first_recent_id FROM job_history WHERE created_at >= $1 GROUP BY job_id
	), classified AS (
		SELECT r.job_id, EXISTS (SELECT 1 FROM job_history h WHERE h.job_id=r.job_id AND h.id<r.first_recent_id) AS previously_known FROM recent r
	)
	SELECT (SELECT count(*) FROM jobs), count(*) FILTER (WHERE NOT previously_known), count(*) FILTER (WHERE previously_known) FROM classified`, since).Scan(&total, &added, &changed)
	return
}

// Jobs lists stored jobs, newest first.
func (s *Store) Jobs(ctx context.Context) ([]Job, error) {
	return s.JobsFiltered(ctx, JobFilters{})
}

// JobFilters narrows the opportunity catalog by explicitly selected values.
type JobFilters struct {
	Query      string
	Levels     []string
	Directions []string
	Languages  []string
	Companies  []string
	Locations  []string
	Types      []string
	WorkModes  []string
	Limit      int
	Cursor     int64
}

// JobsFiltered lists matching jobs in stable, descending insertion order.
func (s *Store) JobsFiltered(ctx context.Context, filter JobFilters) ([]Job, error) {
	args := []any{"%" + strings.TrimSpace(filter.Query) + "%"}
	conditions := []string{"($1 = '%%' OR title ILIKE $1 OR company ILIKE $1)"}
	// Values within a facet are alternatives; different facets must all match.
	for _, item := range []struct {
		column string
		values []string
	}{
		{"level", filter.Levels}, {"direction", filter.Directions}, {"language", filter.Languages},
		{"company", filter.Companies}, {"location", filter.Locations}, {"offer_type", filter.Types}, {"work_mode", filter.WorkModes},
	} {
		if len(item.values) == 0 {
			continue
		}
		values := make([]string, 0, len(item.values))
		for _, value := range item.values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "" {
				values = append(values, value)
			}
		}
		if len(values) == 0 {
			continue
		}
		args = append(args, values)
		position := len(args)
		if item.column == "language" {
			// A posting can publish several languages in one comma-separated field.
			conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM unnest(string_to_array(language, ',')) AS value WHERE lower(trim(value))=ANY($%d::text[]))", position))
		} else {
			conditions = append(conditions, fmt.Sprintf("%s<>'' AND lower(%s)=ANY($%d::text[])", item.column, item.column, position))
		}
	}
	if filter.Cursor > 0 {
		args = append(args, filter.Cursor)
		conditions = append(conditions, fmt.Sprintf("id<$%d", len(args)))
	}
	query := `SELECT id,COALESCE(source_id,0),url,title,company,description,deadline,status,location,work_mode,offer_type,level,weekly_hours,monthly_salary,salary_currency,closed,direction,language,monthly_salary_min,monthly_salary_max,apply_url FROM jobs WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY id DESC`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limitPosition := len(args)
		query += fmt.Sprintf(" LIMIT $%d", limitPosition)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	xs := []Job{}
	for rows.Next() {
		var x Job
		if err := rows.Scan(&x.ID, &x.SourceID, &x.URL, &x.Title, &x.Company, &x.Description, &x.Deadline, &x.Status, &x.Location, &x.WorkMode, &x.Type, &x.Level, &x.Hours, &x.Salary, &x.Currency, &x.Closed, &x.Direction, &x.Language, &x.SalaryMin, &x.SalaryMax, &x.ApplyURL); err != nil {
			return nil, err
		}
		xs = append(xs, x)
	}
	return xs, rows.Err()
}

// ErrSourceURLConflict indicates that another source already owns the URL.
var ErrSourceURLConflict = errors.New("source URL already exists")

// UpdateSource changes source settings without modifying collected jobs.
func (s *Store) UpdateSource(ctx context.Context, id int64, name, url string, enabled bool) error {
	return updateSource(ctx, s.db, id, name, url, enabled)
}

func updateSource(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, id int64, name, url string, enabled bool) error {
	result, err := db.ExecContext(ctx, `UPDATE sources SET name=$2,enabled=CASE WHEN url<>$3 THEN false ELSE $4 END,url=$3 WHERE id=$1`, id, name, url, enabled)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "sources_url_key" {
			return ErrSourceURLConflict
		}
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
	return deleteSource(ctx, s.db, id)
}

func deleteSource(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, id int64) error {
	result, err := db.ExecContext(ctx, `DELETE FROM sources WHERE id=$1`, id)
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
