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
