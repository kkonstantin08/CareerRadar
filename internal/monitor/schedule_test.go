package monitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestNextMoscowRun(t *testing.T) {
	loc := time.FixedZone("MSK", 3*60*60)
	for _, tc := range []struct{ now, want string }{{"2026-11-04T07:59:00+03:00", "2026-11-04T08:00:00+03:00"}, {"2026-11-04T08:00:00+03:00", "2026-11-05T08:00:00+03:00"}} {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := time.Parse(time.RFC3339, tc.want)
		if got := nextRun(now, loc); !got.Equal(want) {
			t.Fatalf("got %s want %s", got, want)
		}
	}
}

func TestExplicitClosureRequiresMatchingSavedJobAndSuccessfulFetch(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("closure_monitor_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn = u.String()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobID := time.Now().UnixNano() % 1000000000000
	jobURL := fmt.Sprintf("https://jobicy.com/jobs/%d-test", jobID)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	defer sqlDB.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, jobURL)
	src, err := db.AddSource(ctx, "Closure test", jobURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	if _, err = db.Observe(ctx, store.Observation{SourceID: src.ID, URL: jobURL, Title: "Go", Company: "Example"}); err != nil {
		t.Fatal(err)
	}
	var fetchErr error
	resultURL := jobURL
	svc := New(db, func(context.Context, string) ([]collector.Job, error) {
		return []collector.Job{{URL: resultURL, SourceURL: fmt.Sprintf("https://jobicy.com/api/jobs/status?ids=%d", jobID), Closed: true}}, fetchErr
	})
	fetchErr = errors.New("source unavailable")
	if _, err = svc.Check(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	list, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, job := range list {
		if job.URL == jobURL {
			id = job.ID
			if job.Closed {
				t.Fatal("failure closed a job")
			}
		}
	}
	if id == 0 {
		t.Fatal("missing job")
	}
	fetchErr = nil
	if problem, err := svc.Check(ctx, src.ID); err != nil || problem == "" {
		t.Fatalf("missing cooldown: %q %v", problem, err)
	}
	if _, err = sqlDB.ExecContext(ctx, `UPDATE jobicy_poll SET polled_at=now()-interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	resultURL = jobURL + "-different"
	if _, err = svc.Check(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	details, err := db.Details(ctx, id)
	if err != nil || details.Job.Closed {
		t.Fatalf("mismatched source closed job %+v: %v", details.Job, err)
	}
	resultURL = jobURL
	if _, err = sqlDB.ExecContext(ctx, `UPDATE jobicy_poll SET polled_at=now()-interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Check(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	details, err = db.Details(ctx, id)
	if err != nil || !details.Job.Closed {
		t.Fatalf("job %+v: %v", details.Job, err)
	}
	if _, err = sqlDB.ExecContext(ctx, `UPDATE jobicy_poll SET polled_at=now()-interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Check(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE event_key LIKE $1`, fmt.Sprintf("closure:%d:%%", id)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("closure notifications %d: %v", count, err)
	}
	defer sqlDB.ExecContext(ctx, `DELETE FROM notifications WHERE event_key LIKE $1`, fmt.Sprintf("closure:%d:%%", id))
}

func TestDailySummaryQueuedOnce(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("monitor_test_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn = u.String()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	day := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer sqlDB.ExecContext(ctx, `DELETE FROM notifications WHERE event_key=$1`, "daily:"+day)
	svc := New(db, func(context.Context, string) ([]collector.Job, error) { return nil, errors.New("test unavailable") })
	if err = svc.runOnce(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err = svc.runOnce(ctx, day); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE event_key=$1`, "daily:"+day).Scan(&n); err != nil || n != 1 {
		t.Fatalf("summary count %d %v", n, err)
	}
}

func TestQueuedTelegramCheckRunsAndCompletes(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("tgworker_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlDB, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err = sqlDB.ExecContext(ctx, `INSERT INTO telegram_link(chat_id) VALUES(123)`); err != nil {
		t.Fatal(err)
	}
	src, err := db.AddSource(ctx, "Example", "https://example.org", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.QueueTelegramCheck(ctx, 42, 123, src.ID); err != nil {
		t.Fatal(err)
	}
	svc := New(db, func(context.Context, string) ([]collector.Job, error) {
		return []collector.Job{{Title: "Engineer", URL: "https://example.org/job"}}, nil
	})
	worked, err := svc.runManualCheck(ctx)
	if err != nil || !worked {
		t.Fatalf("work %t %v", worked, err)
	}
	var state string
	if err = sqlDB.QueryRowContext(ctx, `SELECT state FROM telegram_checks WHERE update_id=42`).Scan(&state); err != nil || state != "done" {
		t.Fatalf("state %s %v", state, err)
	}
	xs, err := db.Jobs(ctx)
	if err != nil || len(xs) != 1 {
		t.Fatalf("jobs %+v %v", xs, err)
	}
}

func TestConcurrentChecksShareGlobalLimit(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("tglimit_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids := []int64{}
	for i := 0; i < 3; i++ {
		src, err := db.AddSource(ctx, "Example", fmt.Sprintf("https://example.org/%d", i), true)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, src.ID)
	}
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	svc := New(db, func(context.Context, string) ([]collector.Job, error) {
		entered <- struct{}{}
		<-release
		return nil, errors.New("unavailable")
	})
	done := make(chan error, 3)
	for _, id := range ids {
		go func(id int64) { _, err := svc.Check(ctx, id); done <- err }(id)
	}
	<-entered
	<-entered
	select {
	case <-entered:
		close(release)
		for range ids {
			<-done
		}
		t.Fatal("more than two source checks run simultaneously")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for range ids {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestManualCheckAllowsZeroUpdateID(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("tgzero_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.ExecContext(ctx, `INSERT INTO telegram_link(chat_id) VALUES(123)`); err != nil {
		t.Fatal(err)
	}
	src, err := db.AddSource(ctx, "Example", "https://example.org", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.QueueTelegramCheck(ctx, 0, 123, src.ID); err != nil {
		t.Fatal(err)
	}
	svc := New(db, func(context.Context, string) ([]collector.Job, error) { return nil, errors.New("source unavailable") })
	worked, err := svc.runManualCheck(ctx)
	if err != nil || !worked {
		t.Fatalf("work %t %v", worked, err)
	}
	var state string
	if err = raw.QueryRowContext(ctx, `SELECT state FROM telegram_checks WHERE update_id=0`).Scan(&state); err != nil || state != "done" {
		t.Fatalf("state %q %v", state, err)
	}
}
