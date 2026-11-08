package monitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type monitorTransport func(*http.Request) (*http.Response, error)

func TestConflictingPostingIdentityMarksSourceForAttention(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://identity-%d.example/jobs", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Conflict", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	defer admin.Exec(`DELETE FROM jobs WHERE source_id=$1`, src.ID)
	posting := fmt.Sprintf("https://jobs.lever.co/example%d/12345678-1234-1234-1234-123456789abc", src.ID)
	for _, raw := range []string{base + "/mirror", posting} {
		if _, err = db.Observe(ctx, store.Observation{SourceID: src.ID, URL: raw, Title: "Engineer"}); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(db, func(context.Context, string) ([]collector.Job, error) {
		return []collector.Job{{URL: base + "/mirror", Title: "Engineer", Company: "Example", Deadline: "2027-01-01", ApplyURL: posting + "/apply"}}, nil
	})
	problem, err := svc.Check(ctx, src.ID)
	if err != nil || !strings.Contains(problem, "conflicting vacancy identities") {
		t.Fatalf("problem=%q err=%v", problem, err)
	}
	sources, err := db.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		if s.ID == src.ID && s.Problem != problem {
			t.Fatalf("saved problem=%q want %q", s.Problem, problem)
		}
	}
}

func (f monitorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

func TestJobicyChecksShareBatch(t *testing.T) {
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
	schema := fmt.Sprintf("jobicy_batch_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.AddSource(ctx, "A", "https://www.jobicy.com:443/jobs/101-a?utm_source=test#detail", true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.AddSource(ctx, "B", "https://jobicy.com/jobs/102-b", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Observe(ctx, store.Observation{SourceID: a.ID, URL: a.URL, Title: "Developer", Company: "Example"}); err != nil {
		t.Fatal(err)
	}
	if changed, err := db.Observe(ctx, store.Observation{SourceID: a.ID, URL: "https://jobicy.com/jobs/101-a", Title: "Developer", Company: "Example"}); err != nil || changed {
		t.Fatalf("URL variant created a duplicate: %v %v", changed, err)
	}
	if _, err = db.ConfirmClosedURL(ctx, a.URL, "https://jobicy.com/api/jobs/status?ids=101"); err != nil {
		t.Fatal(err)
	}
	svc := New(db, func(context.Context, string) ([]collector.Job, error) { t.Fatal("unbatched fetch"); return nil, nil })
	calls := 0
	pageCalls := 0
	svc.SetJobicyBatch(func(_ context.Context, ids []string) (map[string]string, error) {
		calls++
		if len(ids) != 2 {
			t.Fatalf("batch %v", ids)
		}
		return map[string]string{"101": "active", "102": "active"}, nil
	}, func(_ context.Context, raw, status string) ([]collector.Job, error) {
		pageCalls++
		return []collector.Job{{URL: raw, Title: "Developer", Company: "Example", Deadline: "2027-01-01"}}, nil
	})
	if _, err = svc.Check(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.URL == "https://jobicy.com/jobs/101-a" && job.Closed {
			t.Fatal("active Jobicy vacancy remained closed")
		}
	}
	if _, err = svc.Check(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("batch calls %d", calls)
	}
	if _, err = svc.Check(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if pageCalls != 2 {
		t.Fatalf("repeated page request: %d", pageCalls)
	}
	restarted := New(db, func(context.Context, string) ([]collector.Job, error) {
		t.Fatal("unexpected fetch after restart")
		return nil, nil
	})
	restarted.SetJobicyBatch(func(context.Context, []string) (map[string]string, error) {
		t.Fatal("unexpected batch after restart")
		return nil, nil
	}, func(context.Context, string, string) ([]collector.Job, error) {
		t.Fatal("unexpected page after restart")
		return nil, nil
	})
	if _, err = restarted.Check(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGreenhouseNotFoundClosesOnlyTheObservedJob(t *testing.T) {
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
	schema := fmt.Sprintf("greenhouse_closure_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobID := time.Now().UnixNano()%1000000000000 + 1
	jobURL := fmt.Sprintf("https://boards.greenhouse.io/careerradar-test/jobs/%d?gh_jid=%d&utm_source=test#apply", jobID, jobID)
	source, err := db.AddSource(ctx, "Greenhouse job", jobURL, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Observe(ctx, store.Observation{SourceID: source.ID, URL: jobURL, Title: "Go Intern", Company: "Example"}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: monitorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "boards-api.greenhouse.io" {
			t.Fatalf("unexpected host %s", r.URL.Host)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: r}, nil
	})}
	service := New(db, func(ctx context.Context, raw string) ([]collector.Job, error) {
		return collector.Fetch(ctx, client, raw)
	})
	if problem, err := service.Check(ctx, source.ID); err != nil || problem != "" {
		t.Fatalf("check: %q %v", problem, err)
	}
	jobs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || !jobs[0].Closed {
		t.Fatalf("job was not confirmed closed: %+v", jobs)
	}
	details, err := db.Details(ctx, jobs[0].ID)
	if err != nil || len(details.Evidence) < 2 {
		t.Fatalf("closure evidence was not retained: %+v %v", details, err)
	}
	unseenURL := fmt.Sprintf("https://boards.greenhouse.io/careerradar-test/jobs/%d", time.Now().UnixNano()%1000000000000+1)
	unseen, err := db.AddSource(ctx, "Unseen Greenhouse job", unseenURL, true)
	if err != nil {
		t.Fatal(err)
	}
	if problem, err := service.Check(ctx, unseen.ID); err != nil || problem == "" {
		t.Fatalf("unseen 404 must require attention: %q %v", problem, err)
	}
	jobs, err = db.Jobs(ctx)
	if err != nil || len(jobs) != 1 || !jobs[0].Closed {
		t.Fatalf("unobserved Greenhouse URL created a closed vacancy: %+v %v", jobs, err)
	}
}

func TestPublishedProviderListingsReopenSourceClosures(t *testing.T) {
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
	schema := fmt.Sprintf("source_reopen_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
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
	leverURL := "https://jobs.lever.co/careerradar-test/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e"
	greenhouseURL := "https://boards.greenhouse.io/careerradar-test/jobs/123456789"
	cases := []struct {
		name, url, evidence string
	}{
		{"Lever", leverURL, leverURL},
		{"Greenhouse", greenhouseURL, "https://boards-api.greenhouse.io/v1/boards/careerradar-test/jobs/123456789"},
		{"Habr", "https://career.habr.com/vacancies/1000061473", "https://career.habr.com/vacancies/1000061473"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, err := db.AddSource(ctx, tc.name, tc.url, true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.DeleteSource(ctx, source.ID)
			if _, err = db.Observe(ctx, store.Observation{SourceID: source.ID, URL: tc.url, Title: "Go Engineer", Company: "Example"}); err != nil {
				t.Fatal(err)
			}
			if closed, err := db.ConfirmClosedURL(ctx, tc.url, tc.evidence); err != nil || !closed {
				t.Fatalf("close result %v %v", closed, err)
			}
			var id int64
			if err = sqlDB.QueryRowContext(ctx, `SELECT id FROM jobs WHERE url=$1`, tc.url).Scan(&id); err != nil {
				t.Fatal(err)
			}
			defer sqlDB.ExecContext(ctx, `DELETE FROM notifications WHERE event_key LIKE $1 OR event_key LIKE $2`, fmt.Sprintf("closure:%d:%%", id), fmt.Sprintf("reopen:%d:%%", id))
			service := New(db, func(context.Context, string) ([]collector.Job, error) {
				return []collector.Job{{URL: tc.url, SourceURL: tc.url, Title: "Go Engineer", Company: "Example", Deadline: "2027-01-01"}}, nil
			})
			if problem, err := service.Check(ctx, source.ID); err != nil || problem != "" {
				t.Fatalf("check: %q %v", problem, err)
			}
			details, err := db.Details(ctx, id)
			if err != nil || details.Job.Closed {
				t.Fatalf("listing remained closed after source republished it: %+v %v", details.Job, err)
			}
		})
	}
}

func TestMonitorPersistsSalaryRangeFromHTML(t *testing.T) {
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
	schema := fmt.Sprintf("salary_monitor_%d", time.Now().UnixNano())
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
	src, err := db.AddSource(ctx, "Salary", "https://career.habr.com/vacancies/1000000001", true)
	if err != nil {
		t.Fatal(err)
	}
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Junior Go Developer","hiringOrganization":{"name":"Example"},"validThrough":"2027-01-01","jobLocationType":"TELECOMMUTE","jobLocation":[{"address":"Казань"}],"baseSalary":{"currency":"RUR","value":{"minValue":80000,"maxValue":120000,"unitText":"MONTH"}}}</script>`
	fetchCalls := 0
	svc := New(db, func(_ context.Context, raw string) ([]collector.Job, error) {
		fetchCalls++
		found, err := collector.Parse(strings.NewReader(body), raw)
		if err == nil && len(found) == 1 {
			found[0].ApplyURL = "https://example.org/apply"
		}
		return found, err
	})
	if problem, err := svc.Check(ctx, src.ID); err != nil || problem != "" {
		t.Fatalf("check %q %v", problem, err)
	}
	got, err := db.Jobs(ctx)
	if err != nil || len(got) != 1 || got[0].SalaryMin == nil || got[0].SalaryMax == nil || *got[0].SalaryMin != 80000 || *got[0].SalaryMax != 120000 || got[0].ApplyURL != "https://example.org/apply" {
		t.Fatalf("jobs %+v %v", got, err)
	}
	if got[0].Currency != "RUB" || got[0].Location != "Казань" || got[0].WorkMode != "remote" || got[0].Level != "Junior" {
		t.Fatalf("Habr facts=%+v", got[0])
	}
	body = strings.Replace(body, `"jobLocation":[{"address":"Казань"}]`, `"jobLocation":[{"address":"Казань"},{"address":"Москва"}]`, 1)
	if problem, err := svc.Check(ctx, src.ID); err != nil || problem == "" {
		t.Fatalf("ambiguous location problem=%q err=%v", problem, err)
	}
	got, err = db.Jobs(ctx)
	if err != nil || len(got) != 1 || got[0].Location != "" || got[0].Closed {
		t.Fatalf("uncertain job=%+v err=%v", got, err)
	}
	release, err := svc.LockHabr(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	before := fetchCalls
	if _, err = svc.Check(blocked, src.ID); !errors.Is(err, context.DeadlineExceeded) || fetchCalls != before {
		t.Fatalf("gate err=%v calls=%d before=%d", err, fetchCalls, before)
	}
	board, err := db.AddSource(ctx, "Habr board", "https://career.habr.com/vacancies", true)
	if err != nil {
		t.Fatal(err)
	}
	blockedBoard, cancelBoard := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelBoard()
	if _, err = svc.Check(blockedBoard, board.ID); !errors.Is(err, context.DeadlineExceeded) || fetchCalls != before {
		t.Fatalf("board gate err=%v calls=%d before=%d", err, fetchCalls, before)
	}
	other, err := db.AddSource(ctx, "Redirecting board", "https://example.org/careers", true)
	if err != nil {
		t.Fatal(err)
	}
	blockedOther, cancelOther := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelOther()
	if _, err = svc.Check(blockedOther, other.ID); !errors.Is(err, context.DeadlineExceeded) || fetchCalls != before {
		t.Fatalf("redirect gate err=%v calls=%d before=%d", err, fetchCalls, before)
	}
}

func TestJobicyRetainsSuccessfulChunksWhenLaterChunkFails(t *testing.T) {
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
	schema := fmt.Sprintf("jobicy_partial_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var first store.Source
	for i := 1; i <= 101; i++ {
		src, err := db.AddSource(ctx, "Job", fmt.Sprintf("https://jobicy.com/jobs/%d-example", i), true)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			first = src
		}
	}
	svc := New(db, nil)
	svc.SetJobicyBatch(func(_ context.Context, ids []string) (map[string]string, error) {
		if len(ids) == 1 {
			return nil, errors.New("second batch unavailable")
		}
		m := map[string]string{}
		for _, id := range ids {
			m[id] = "active"
		}
		return m, nil
	}, func(_ context.Context, raw, status string) ([]collector.Job, error) {
		return []collector.Job{{URL: raw, Title: "Developer", Company: "Example", Deadline: "2027-01-01"}}, nil
	})
	if problem, err := svc.Check(ctx, first.ID); err != nil || problem != "" {
		t.Fatalf("first chunk lost: %q %v", problem, err)
	}
	status, err := db.JobicyStatus(ctx, "1")
	if err != nil || status != "active" {
		t.Fatalf("saved status %q %v", status, err)
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
	src, err := db.AddSource(ctx, "Test", "https://daily.example", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Observe(ctx, store.Observation{SourceID: src.ID, URL: src.URL + "/job", Title: "New"}); err != nil {
		t.Fatal(err)
	}
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
	var message string
	if err = sqlDB.QueryRowContext(ctx, `SELECT message FROM notifications WHERE event_key=$1`, "daily:"+day).Scan(&message); err != nil || !strings.Contains(message, "новых — 1, изменённых — 0") {
		t.Fatalf("summary %q %v", message, err)
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

func TestConcurrentSourcesDoNotOverwriteNewerObservation(t *testing.T) {
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
	schema := fmt.Sprintf("ordering_%d", time.Now().UnixNano())
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
	old, err := db.AddSource(ctx, "Old", "https://old.example/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := db.AddSource(ctx, "New", "https://new.example/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	svc := New(db, func(_ context.Context, source string) ([]collector.Job, error) {
		title := "New title"
		if source == old.URL {
			close(entered)
			<-release
			title = "Old title"
		}
		job := collector.Job{URL: "https://career.habr.com/vacancies/1000061473", SourceURL: "https://career.habr.com/vacancies/1000061473", Title: title, Company: "Example", Deadline: "2027-01-01"}
		if source == old.URL {
			job.Description = "Confirmed requirements"
			job.Level = "Junior"
			job.Type = "Internship"
			job.Location = "Kazan"
		}
		return []collector.Job{job}, nil
	})
	done := make(chan error, 1)
	go func() { _, e := svc.Check(ctx, old.ID); done <- e }()
	<-entered
	if _, err = svc.Check(ctx, newer.ID); err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	jobs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Title != "New title" || jobs[0].Description != "Confirmed requirements" || jobs[0].Level != "" || jobs[0].Type != "" || jobs[0].Location != "" {
		t.Fatalf("stale observation overwrote newer result: %+v", jobs)
	}
}

type observedCancellation struct {
	context.Context
	waiting chan struct{}
}

func (c observedCancellation) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestConcurrentSameSourceCheckHonorsCancellation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.AddSource(ctx, "Cancellation", fmt.Sprintf("https://cancel.example/%d", time.Now().UnixNano()), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, source.ID)
	entered := make(chan struct{})
	release := make(chan struct{})
	svc := New(db, func(context.Context, string) ([]collector.Job, error) {
		close(entered)
		<-release
		return nil, errors.New("unavailable")
	})
	first := make(chan error, 1)
	go func() { _, err := svc.Check(ctx, source.ID); first <- err }()
	select {
	case <-entered:
	case err := <-first:
		t.Fatalf("first check failed before fetch: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first check did not reach fetch")
	}
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	second := make(chan error, 1)
	waiting := make(chan struct{}, 1)
	go func() { _, err := svc.Check(observedCancellation{cancelled, waiting}, source.ID); second <- err }()
	select {
	case <-waiting:
	case err := <-second:
		close(release)
		<-first
		t.Fatalf("check did not wait: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		<-first
		t.Fatal("check did not reach cancellation wait")
	}
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			close(release)
			<-first
			t.Fatalf("got %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(release)
		<-first
		<-second
		t.Fatal("cancelled check waited for prior source check")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestJobicyBatchWaitHonorsCancellation(t *testing.T) {
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
	schema := fmt.Sprintf("cancel_jobicy_%d", time.Now().UnixNano())
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
	entered := make(chan struct{})
	release := make(chan struct{})
	svc := New(db, nil)
	svc.SetJobicyBatch(func(context.Context, []string) (map[string]string, error) {
		close(entered)
		<-release
		return map[string]string{}, nil
	}, nil)
	first := make(chan error, 1)
	go func() {
		_, err := svc.jobicyStatus(ctx, "101", []store.Source{{URL: "https://jobicy.com/jobs/101-test", Enabled: true}})
		first <- err
	}()
	select {
	case <-entered:
	case err := <-first:
		t.Fatalf("first check failed before fetch: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first check did not reach fetch")
	}
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	second := make(chan error, 1)
	waiting := make(chan struct{}, 1)
	go func() {
		_, err := svc.jobicyStatus(observedCancellation{cancelled, waiting}, "102", nil)
		second <- err
	}()
	select {
	case <-waiting:
	case err := <-second:
		close(release)
		<-first
		t.Fatalf("batch did not wait: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		<-first
		t.Fatal("batch did not reach cancellation wait")
	}
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			close(release)
			<-first
			t.Fatalf("got %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(release)
		<-first
		<-second
		t.Fatal("cancelled batch waited for previous batch")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}
