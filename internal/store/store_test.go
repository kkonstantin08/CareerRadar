package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/kkonstantin08/CareerRadar/internal/discovery"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestSourcesAndRepeatedObservations(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	base := fmt.Sprintf("https://example.org/%d", time.Now().UnixNano())
	source, err := db.AddSource(ctx, "Example", base, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.db.ExecContext(context.Background(), `DELETE FROM jobs WHERE url=$1`, base+"/1")
		_, _ = db.db.ExecContext(context.Background(), `DELETE FROM sources WHERE id=$1`, source.ID)
	})
	again, err := db.AddSource(ctx, "Example", base, true)
	if err != nil {
		t.Fatal(err)
	}
	if source.ID != again.ID {
		t.Fatal("duplicate source")
	}
	job := Observation{SourceID: source.ID, URL: base + "/1", Title: "Junior Go", Company: "Example"}
	first, err := db.Observe(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := db.Observe(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if !first || repeated {
		t.Fatalf("new, repeat = %t, %t", first, repeated)
	}
	job.Title = "Junior Go Engineer"
	changed, err := db.Observe(ctx, job)
	if err != nil || !changed {
		t.Fatalf("change = %t, %v", changed, err)
	}
	if err := db.RecordCheck(ctx, source.ID, "access denied"); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range jobs {
		if j.URL == job.URL && j.Title == job.Title {
			found = true
		}
	}
	if !found {
		t.Fatalf("job missing: %+v", jobs)
	}
}

func TestEvidenceSurvivesSourceDeletion(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://evidence.example.org/%d", time.Now().UnixNano())
	a, err := db.AddSource(ctx, "A", base+"/a", true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.AddSource(ctx, "B", base+"/b", true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, base+"/job")
	defer db.DeleteSource(ctx, b.ID)
	for _, source := range []Source{a, b} {
		if _, err := db.Observe(ctx, Observation{SourceID: source.ID, URL: base + "/job", Title: "Engineer"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DeleteSource(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM job_evidence WHERE job_url=$1 AND page_url IN ($2,$3)`, base+"/job", a.URL, b.URL).Scan(&n); err != nil || n != 2 {
		t.Fatalf("evidence count %d: %v", n, err)
	}
}

func TestTelegramLinkIsOneUseAndCannotReplaceOwner(t *testing.T) {
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
	schema := fmt.Sprintf("telegram_test_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.ExecContext(ctx, `TRUNCATE telegram_link, telegram_tokens`); err != nil {
		t.Fatal(err)
	}
	defer db.db.ExecContext(ctx, `TRUNCATE telegram_link, telegram_tokens`)
	code, err := db.NewLinkCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.LinkTelegram(ctx, code, 12345); err != nil {
		t.Fatal(err)
	}
	if err = db.LinkTelegram(ctx, code, 12345); err == nil {
		t.Fatal("reused code")
	}
	code, err = db.NewLinkCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.LinkTelegram(ctx, code, 99999); err == nil {
		t.Fatal("replaced linked owner")
	}
	chat, err := db.TelegramChat(ctx)
	if err != nil || chat != 12345 {
		t.Fatalf("chat %d %v", chat, err)
	}
}

func TestNotificationClaimIsNotRepeated(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fmt.Sprintf("event-%d", time.Now().UnixNano())
	defer db.db.ExecContext(ctx, `DELETE FROM notifications WHERE event_key=$1`, key)
	if err := db.QueueNotification(ctx, key, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueNotification(ctx, key, "hello"); err != nil {
		t.Fatal(err)
	}
	first, err := db.ClaimNotification(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if first != "hello" {
		t.Fatalf("first %q", first)
	}
	second, err := db.ClaimNotification(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if second != "" {
		t.Fatalf("repeated %q", second)
	}
}

func TestChangedJobQueuesOnlyOneNotificationPerVersion(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://notify.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Notify", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, base+"/job")
	x := Observation{SourceID: src.ID, URL: base + "/job", Title: "Junior Go"}
	for i := 0; i < 2; i++ {
		if _, err = db.Observe(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE message LIKE $1`, "%"+x.URL+"%").Scan(&n); err != nil || n != 1 {
		t.Fatalf("notifications: %d %v", n, err)
	}
	defer db.db.ExecContext(ctx, `DELETE FROM notifications WHERE message LIKE $1`, "%"+x.URL+"%")
}

func TestIdenticalSourceWarningsAreNotRepeated(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://warning.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Warning", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	defer db.db.ExecContext(ctx, `DELETE FROM notifications WHERE message LIKE $1`, "%"+base+"%")
	for i := 0; i < 2; i++ {
		if err = db.RecordCheck(ctx, src.ID, "HTTP 403"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE message LIKE $1`, "%"+base+"%").Scan(&n); err != nil || n != 1 {
		t.Fatalf("warnings %d %v", n, err)
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := jobs.Preferences{Levels: []string{"Junior", "Intern"}, Languages: []string{"Go"}}
	if err = db.SavePreferences(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.Preferences(ctx)
	if err != nil || len(got.Levels) != 2 || len(got.Languages) != 1 || got.Languages[0] != "Go" {
		t.Fatalf("preferences %+v %v", got, err)
	}
}

func TestTrackingParametersDoNotDuplicateJobs(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://dedupe.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Dedupe", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, base+"/job?id=1")
	first, err := db.Observe(ctx, Observation{SourceID: src.ID, URL: base + "/job?id=1&utm_source=mail#apply", Title: "Engineer"})
	if err != nil || !first {
		t.Fatalf("first %t %v", first, err)
	}
	repeated, err := db.Observe(ctx, Observation{SourceID: src.ID, URL: base + "/job?id=1&utm_campaign=spring", Title: "Engineer"})
	if err != nil || repeated {
		t.Fatalf("repeat %t %v", repeated, err)
	}
}

func TestUnsuitableJobDoesNotQueueNotification(t *testing.T) {
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
	schema := fmt.Sprintf("filter_test_%d", time.Now().UnixNano())
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
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.SavePreferences(ctx, jobs.Preferences{Companies: []string{"Wanted"}}); err != nil {
		t.Fatal(err)
	}
	src, err := db.AddSource(ctx, "Example", "https://example.org", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: "https://example.org/job", Title: "Engineer", Company: "Different"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("notifications %d %v", n, err)
	}
}

func TestConcurrentObservationsCreateOneRevision(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://race.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Race", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, base+"/job")
	const count = 8
	out := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() {
			_, err := db.Observe(ctx, Observation{SourceID: src.ID, URL: base + "/job", Title: "Engineer"})
			out <- err
		}()
	}
	for i := 0; i < count; i++ {
		if err = <-out; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM job_history h JOIN jobs j ON j.id=h.job_id WHERE j.url=$1`, base+"/job").Scan(&n); err != nil || n != 1 {
		t.Fatalf("revisions %d %v", n, err)
	}
}

func TestOpenDoesNotLockExistingTablesAfterMigration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `LOCK TABLE jobs IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	limited, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	other, err := Open(limited, dsn)
	if err != nil {
		t.Fatalf("opening migrated database blocked on jobs: %v", err)
	}
	other.Close()
}

func TestTelegramUpdatesAreClaimedOnce(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id := time.Now().UnixNano()
	first, err := db.ClaimTelegramUpdate(ctx, id)
	if err != nil || !first {
		t.Fatalf("first %v %v", first, err)
	}
	again, err := db.ClaimTelegramUpdate(ctx, id)
	if err != nil || again {
		t.Fatalf("again %v %v", again, err)
	}
}

func TestObservationPreservesSalaryAndHours(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://hours.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Hours", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, base+"/job")
	hours := 20
	salary := int64(70000)
	x := Observation{SourceID: src.ID, URL: base + "/job", Title: "Intern", Hours: &hours, Salary: &salary, Currency: "RUB"}
	if _, err = db.Observe(ctx, x); err != nil {
		t.Fatal(err)
	}
	x.Hours = nil
	x.Salary = nil
	x.Currency = ""
	if changed, err := db.Observe(ctx, x); err != nil || changed {
		t.Fatalf("repeat %v %v", changed, err)
	}
	list, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range list {
		if j.URL == x.URL {
			if j.Hours == nil || *j.Hours != 20 || j.Salary == nil || *j.Salary != 70000 || j.Currency != "RUB" {
				t.Fatalf("job %+v", j)
			}
			return
		}
	}
	t.Fatal("job missing")
}

func TestChangingSourceURLRequiresConfirmation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://changed.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Example", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	if err = db.UpdateSource(ctx, src.ID, "Example", base+"/new", true); err != nil {
		t.Fatal(err)
	}
	xs, err := db.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		if x.ID == src.ID {
			if x.Enabled {
				t.Fatal("new URL enabled without confirmation")
			}
			return
		}
	}
	t.Fatal("source missing")
}

func TestConfirmedClosureRequiresEvidenceAndIsIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://closure.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Closure", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, base+"/job")
	_, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: base + "/job", Title: "Intern"})
	if err != nil {
		t.Fatal(err)
	}
	xs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, x := range xs {
		if x.URL == base+"/job" {
			id = x.ID
		}
	}
	if id == 0 {
		t.Fatal("job missing")
	}
	if err = db.ConfirmClosed(ctx, id, ""); err == nil {
		t.Fatal("missing evidence accepted")
	}
	if err = db.ConfirmClosed(ctx, id, base+"/job"); err != nil {
		t.Fatal(err)
	}
	if err = db.ConfirmClosed(ctx, id, base+"/job"); err != nil {
		t.Fatal(err)
	}
	detail, err := db.Details(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Job.Closed {
		t.Fatal("closure not saved")
	}
	var n int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE event_key LIKE $1`, fmt.Sprintf("closure:%d:%%", id)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notifications %d %v", n, err)
	}
	defer db.db.ExecContext(ctx, `DELETE FROM notifications WHERE event_key LIKE $1`, fmt.Sprintf("closure:%d:%%", id))
}

func TestOwnerCanCorrectClosedStatus(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://reopen.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Reopen", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: base + "/job", Title: "Intern"}); err != nil {
		t.Fatal(err)
	}
	xs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		if x.URL == base+"/job" {
			if err = db.ConfirmClosed(ctx, x.ID, x.URL); err != nil {
				t.Fatal(err)
			}
			if err = db.Reopen(ctx, x.ID); err != nil {
				t.Fatal(err)
			}
			d, err := db.Details(ctx, x.ID)
			if err != nil || d.Job.Closed {
				t.Fatalf("detail %+v %v", d, err)
			}
			return
		}
	}
	t.Fatal("job missing")
}

func TestUncertainNotificationCanBeResolvedWithoutResending(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fmt.Sprintf("uncertain-%d", time.Now().UnixNano())
	defer db.db.ExecContext(ctx, `DELETE FROM notifications WHERE event_key=$1`, key)
	if err = db.QueueNotification(ctx, key, "message"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNotification(ctx, key); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = db.db.QueryRowContext(ctx, `UPDATE notifications SET state='unknown' WHERE event_key=$1 RETURNING id`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err = db.ResolveNotification(ctx, id, "sent"); err != nil {
		t.Fatal(err)
	}
	if err = db.ResolveNotification(ctx, id, "retry"); err == nil {
		t.Fatal("resolved message was eligible for retry")
	}
	var state string
	if err = db.db.QueryRowContext(ctx, `SELECT state FROM notifications WHERE id=$1`, id).Scan(&state); err != nil || state != "sent" {
		t.Fatalf("state %s %v", state, err)
	}
}

func TestStaleSendingIsMarkedUnknownWithoutRetry(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fmt.Sprintf("stale-%d", time.Now().UnixNano())
	defer db.db.ExecContext(ctx, `DELETE FROM notifications WHERE event_key=$1`, key)
	if err = db.QueueNotification(ctx, key, "message"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNotification(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE notifications SET claimed_at=now()-interval '3 minutes' WHERE event_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if err = db.RecoverStaleNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.db.QueryRowContext(ctx, `SELECT state FROM notifications WHERE event_key=$1`, key).Scan(&state); err != nil || state != "unknown" {
		t.Fatalf("state %q %v", state, err)
	}
}

func TestDiscoveredJobsPersistWithoutActivatingSources(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	link := fmt.Sprintf("https://remotive.com/remote-jobs/test-%d", time.Now().UnixNano())
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, link)
	result := discovery.Result{Title: "Go Engineer", Company: "Example", URL: link, Source: "Remotive", Location: "Worldwide"}
	for i := 0; i < 2; i++ {
		if err = db.SaveDiscovered(ctx, []discovery.Result{result}); err != nil {
			t.Fatal(err)
		}
	}
	xs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, j := range xs {
		if j.URL == link {
			n++
			if j.WorkMode != "remote" || j.Location != "Worldwide" {
				t.Fatalf("job %+v", j)
			}
		}
	}
	if n != 1 {
		t.Fatalf("jobs %d", n)
	}
	sources, err := db.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range sources {
		if src.URL == "https://remotive.com/remote-jobs" && src.Enabled {
			t.Fatal("discovery source activated")
		}
	}
}

func TestOldNotificationAttemptCannotFinishNewAttempt(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fmt.Sprintf("attempt-%d", time.Now().UnixNano())
	defer db.db.ExecContext(ctx, `DELETE FROM notifications WHERE event_key=$1`, key)
	if err = db.QueueNotification(ctx, key, "message"); err != nil {
		t.Fatal(err)
	}
	var id, first, second int64
	if err = db.db.QueryRowContext(ctx, `UPDATE notifications SET state='sending',attempt=attempt+1 WHERE event_key=$1 RETURNING id,attempt`, key).Scan(&id, &first); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE notifications SET state='unknown' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = db.ResolveNotification(ctx, id, "retry"); err != nil {
		t.Fatal(err)
	}
	if err = db.db.QueryRowContext(ctx, `UPDATE notifications SET state='sending',attempt=attempt+1 WHERE id=$1 RETURNING attempt`, id).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishNotification(ctx, id, first, true); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.db.QueryRowContext(ctx, `SELECT state FROM notifications WHERE id=$1`, id).Scan(&state); err != nil || state != "sending" {
		t.Fatalf("old attempt changed new state %s %v", state, err)
	}
	if err = db.FinishNotification(ctx, id, second, true); err != nil {
		t.Fatal(err)
	}
}

func TestObservationRetainsDirectionAndLanguage(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://skills.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Skills", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	defer db.db.ExecContext(ctx, `DELETE FROM jobs WHERE url=$1`, base+"/job")
	x := Observation{SourceID: src.ID, URL: base + "/job", Direction: "Backend", Language: "Go"}
	if _, err = db.Observe(ctx, x); err != nil {
		t.Fatal(err)
	}
	x.Direction = ""
	x.Language = ""
	if changed, err := db.Observe(ctx, x); err != nil || changed {
		t.Fatalf("recheck %t %v", changed, err)
	}
	list, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range list {
		if j.URL == x.URL {
			if j.Direction != "Backend" || j.Language != "Go" {
				t.Fatalf("job %+v", j)
			}
			return
		}
	}
	t.Fatal("missing job")
}

func TestTelegramStatusAndAcknowledgementCommitTogether(t *testing.T) {
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
	schema := fmt.Sprintf("tgatomic_%d", time.Now().UnixNano())
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
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.db.ExecContext(ctx, `INSERT INTO telegram_link(chat_id) VALUES(123)`); err != nil {
		t.Fatal(err)
	}
	src, err := db.AddSource(ctx, "test", "https://example.org", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: "https://example.org/job"}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = db.db.QueryRowContext(ctx, `SELECT id FROM jobs`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ApplyTelegramStatus(ctx, 1, 999, id, "Откликнулся"); err == nil {
		t.Fatal("unauthorized update accepted")
	}
	done, err := db.TelegramUpdateProcessed(ctx, 1)
	if err != nil || done {
		t.Fatalf("ack persisted on failure %v %v", done, err)
	}
	first, err := db.ApplyTelegramStatus(ctx, 1, 123, id, "Откликнулся")
	if err != nil || !first {
		t.Fatalf("first %v %v", first, err)
	}
	again, err := db.ApplyTelegramStatus(ctx, 1, 123, id, "Отказ")
	if err != nil || again {
		t.Fatalf("replay %v %v", again, err)
	}
	d, err := db.Details(ctx, id)
	if err != nil || d.Job.Status != "Откликнулся" {
		t.Fatalf("status %+v %v", d.Job, err)
	}
}
