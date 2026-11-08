package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/store"
)

// TestPublicMonitorSmoke is opt-in and stores its observations only in a temporary schema.
func TestPublicMonitorSmoke(t *testing.T) {
	sourceURL, dsn := os.Getenv("SMOKE_MONITOR_URL"), os.Getenv("TEST_DATABASE_URL")
	if sourceURL == "" || dsn == "" {
		t.Skip("SMOKE_MONITOR_URL and TEST_DATABASE_URL are required")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("monitor_smoke_%d", time.Now().UnixNano())
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
	source, err := db.AddSource(ctx, "Public smoke", sourceURL, true)
	if err != nil {
		t.Fatal(err)
	}
	client := collector.Client()
	svc := New(db, func(ctx context.Context, raw string) ([]collector.Job, error) {
		return collector.Fetch(ctx, client, raw)
	})
	if problem, err := svc.Check(ctx, source.ID); err != nil || problem != "" {
		t.Fatalf("first check: problem=%q error=%v", problem, err)
	}
	jobs, err := db.Jobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Title == "" || jobs[0].Closed {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	sqlDB, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var history, notices int
	if err = sqlDB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM job_history),(SELECT count(*) FROM notifications)`).Scan(&history, &notices); err != nil {
		t.Fatal(err)
	}
	if problem, err := svc.Check(ctx, source.ID); err != nil || problem != "" {
		t.Fatalf("second check: problem=%q error=%v", problem, err)
	}
	var historyAfter, noticesAfter int
	if err = sqlDB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM job_history),(SELECT count(*) FROM notifications)`).Scan(&historyAfter, &noticesAfter); err != nil {
		t.Fatal(err)
	}
	if historyAfter != history || noticesAfter != notices {
		t.Fatalf("repeat changed history %d->%d or notifications %d->%d", history, historyAfter, notices, noticesAfter)
	}
	t.Logf("one public vacancy saved; repeat produced no additional revisions or notifications")
}
