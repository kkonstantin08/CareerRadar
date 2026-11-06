package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kkonstantin08/CareerRadar/migrations"
)

func identityTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("identity_%d", time.Now().UnixNano())
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); _ = admin.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPostingAliasesDeduplicateConcurrentSourcesAndRetainState(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	a, err := db.AddSource(ctx, "Company", "https://example.org/careers", true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.AddSource(ctx, "Mirror", "https://mirror.example/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	posting := "https://jobs.lever.co/example/12345678-1234-1234-1234-123456789abc"
	observations := []Observation{
		{SourceID: a.ID, URL: "https://example.org/jobs/one", ApplyURL: posting + "/apply", Title: "Intern", Company: "Example"},
		{SourceID: b.ID, URL: "https://mirror.example/jobs/two", ApplyURL: posting + "/apply", Title: "Intern", Company: "Example"},
	}
	var wg sync.WaitGroup
	for _, observation := range observations {
		wg.Go(func() {
			if _, err := db.Observe(ctx, observation); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
	id := all[0].ID
	if err = db.SetStatus(ctx, id, "Откликнулся"); err != nil {
		t.Fatal(err)
	}
	// An incomplete later observation must keep its established identity and application status.
	x := observations[1]
	x.ApplyURL = ""
	x.Description = "Updated requirements"
	if _, err = db.Observe(ctx, x); err != nil {
		t.Fatal(err)
	}
	all, err = db.Jobs(ctx)
	if err != nil || len(all) != 1 || all[0].Status != "Откликнулся" || all[0].Description != x.Description {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
	d, err := db.Details(ctx, id)
	if err != nil || len(d.History) != 2 {
		t.Fatalf("details=%+v err=%v", d, err)
	}
	for _, observation := range observations {
		found := false
		for _, evidence := range d.Evidence {
			if evidence == observation.URL {
				found = true
			}
		}
		if !found {
			t.Errorf("missing evidence %s: %v", observation.URL, d.Evidence)
		}
	}
	var count int
	if err = db.db.QueryRow(`SELECT count(*) FROM notifications WHERE event_key LIKE $1`, fmt.Sprintf("job:%d:%%", id)).Scan(&count); err != nil || count != 2 {
		t.Fatalf("notifications=%d err=%v", count, err)
	}
	closed, err := db.ConfirmClosedURL(ctx, posting, posting)
	if err != nil || !closed {
		t.Fatalf("closure=%t err=%v", closed, err)
	}
	if err = db.ReopenLever(ctx, posting, "https://api.lever.co/v0/postings/example?mode=xml"); err != nil {
		t.Fatal(err)
	}
	d, err = db.Details(ctx, id)
	if err != nil || d.Job.Closed {
		t.Fatalf("reopen=%+v err=%v", d.Job, err)
	}
	jobicyURL := "https://jobicy.com/jobs/123456-role"
	jobicy := observations[0]
	jobicy.URL = jobicyURL
	if _, err = db.Observe(ctx, jobicy); err != nil {
		t.Fatal(err)
	}
	jobicyEvidence := "https://jobicy.com/api/jobs/status?ids=123456"
	if closed, err = db.ConfirmClosedURL(ctx, jobicyURL, jobicyEvidence); err != nil || !closed {
		t.Fatalf("Jobicy closure=%t err=%v", closed, err)
	}
	if err = db.ReopenJobicy(ctx, jobicyURL, jobicyEvidence); err != nil {
		t.Fatal(err)
	}
	d, err = db.Details(ctx, id)
	if err != nil || d.Job.Closed {
		t.Fatalf("Jobicy alias reopening failed: %+v err=%v", d.Job, err)
	}
}

func TestGenericApplicationFormsDoNotMergeRoles(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	src, err := db.AddSource(ctx, "Example", "https://example.org/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"one", "two"} {
		if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: src.URL + "/" + path, Title: "Engineer", Company: "Example", ApplyURL: "https://example.org/apply"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
}

func TestConflictingExistingAliasesPreserveBothJobs(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	src, err := db.AddSource(ctx, "Example", "https://example.org/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	posting := "https://jobs.ashbyhq.com/example/12345678-1234-1234-1234-123456789abc/application"
	a := Observation{SourceID: src.ID, URL: src.URL + "/one", Title: "Engineer"}
	b := a
	b.URL = src.URL + "/two"
	for _, x := range []Observation{a, b} {
		if _, err = db.Observe(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	a.ApplyURL = posting
	if _, err = db.Observe(ctx, a); err != nil {
		t.Fatal(err)
	}
	b.ApplyURL = posting
	if _, err = db.Observe(ctx, b); !errors.Is(err, ErrAmbiguousJobIdentity) {
		t.Fatalf("conflict err=%v", err)
	}
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
}

func TestProviderPostingURLsAndMirrorsShareOneRecord(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	src, err := db.AddSource(ctx, "Example", "https://example.org/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	const id = "12345678-1234-1234-1234-123456789abc"
	for _, provider := range []struct{ posting, apply string }{
		{"https://job-boards.greenhouse.io/example/jobs/123?gh_jid=123", "https://boards.greenhouse.io/example/jobs/123"},
		{"https://jobs.lever.co/example/" + id, "https://jobs.lever.co/example/" + id + "/apply"},
		{"https://jobs.ashbyhq.com/example/" + id, "https://jobs.ashbyhq.com/example/" + id + "/application"},
	} {
		mirror := Observation{SourceID: src.ID, URL: src.URL + "/" + url.QueryEscape(provider.posting), Title: "Engineer", ApplyURL: provider.apply}
		if _, err = db.Observe(ctx, mirror); err != nil {
			t.Fatal(err)
		}
		direct := mirror
		direct.URL = provider.posting
		direct.ApplyURL = ""
		if changed, err := db.Observe(ctx, direct); err != nil || changed {
			t.Fatalf("direct created a revision: changed=%t err=%v", changed, err)
		}
	}
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
}

func TestAliasMigrationRetainsExistingApplicationState(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	src, err := db.AddSource(ctx, "Existing", "https://existing.example/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	posting := "https://jobs.lever.co/example/12345678-1234-1234-1234-123456789abc"
	if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: src.URL + "/one", Title: "Engineer", ApplyURL: posting + "/apply"}); err != nil {
		t.Fatal(err)
	}
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
	id := all[0].ID
	if err = db.SetStatus(ctx, id, "Собеседование"); err != nil {
		t.Fatal(err)
	}
	// Recreate the previous schema state inside this test's isolated database schema.
	if _, err = db.db.Exec(`DROP TABLE job_aliases`); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(migrations.JobAliases))
	if _, err = db.db.Exec(`DELETE FROM schema_migrations WHERE checksum=$1`, sum[:]); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db.db); err != nil {
		t.Fatal(err)
	}
	if changed, err := db.Observe(ctx, Observation{SourceID: src.ID, URL: posting, Title: "Engineer"}); err != nil || changed {
		t.Fatalf("first ATS recheck changed=%t err=%v", changed, err)
	}
	var aliasID int64
	if err = db.db.QueryRow(`SELECT job_id FROM job_aliases WHERE alias_url=$1`, all[0].URL).Scan(&aliasID); err != nil || aliasID != id {
		t.Fatalf("alias=%d err=%v", aliasID, err)
	}
	d, err := db.Details(ctx, id)
	if err != nil || d.Job.Status != "Собеседование" || len(d.History) != 1 {
		t.Fatalf("details=%+v err=%v", d, err)
	}
}

func TestReusedPageDoesNotMergeDifferentPostingIDs(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	src, err := db.AddSource(ctx, "Example", "https://example.org/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	x := Observation{SourceID: src.ID, URL: src.URL + "/current", Title: "First role", ApplyURL: "https://jobs.lever.co/example/12345678-1234-1234-1234-123456789abc/apply"}
	if _, err = db.Observe(ctx, x); err != nil {
		t.Fatal(err)
	}
	x.ApplyURL = "https://jobs.lever.co/example/12345678-1234-1234-1234-123456789abd/apply"
	x.Title = "Second role"
	if _, err = db.Observe(ctx, x); !errors.Is(err, ErrAmbiguousJobIdentity) {
		t.Fatalf("replacement err=%v", err)
	}
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 1 || all[0].Title != "First role" {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
}

func TestAliasMigrationBlocksAmbiguousExistingIdentities(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	src, err := db.AddSource(ctx, "Example", "https://example.org/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"one", "two"} {
		if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: src.URL + "/" + path, Title: "Engineer"}); err != nil {
			t.Fatal(err)
		}
	}
	posting := "https://jobs.lever.co/example/12345678-1234-1234-1234-123456789abc"
	if _, err = db.db.Exec(`UPDATE jobs SET apply_url=$1`, posting+"/apply"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`DROP TABLE job_aliases`); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(migrations.JobAliases))
	if _, err = db.db.Exec(`DELETE FROM schema_migrations WHERE checksum=$1`, sum[:]); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db.db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: posting, Title: "Engineer"}); !errors.Is(err, ErrAmbiguousJobIdentity) {
		t.Fatalf("ambiguous migration err=%v", err)
	}
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
}

func TestAliasMigrationBlocksConflictingIDsWithinLegacyRow(t *testing.T) {
	db := identityTestStore(t)
	ctx := context.Background()
	src, err := db.AddSource(ctx, "Example", "https://example.org/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	a := "https://jobs.lever.co/example/12345678-1234-1234-1234-123456789abc"
	b := "https://jobs.lever.co/example/12345678-1234-1234-1234-123456789abd"
	if _, err = db.Observe(ctx, Observation{SourceID: src.ID, URL: a, Title: "Engineer"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`UPDATE jobs SET apply_url=$1`, b+"/apply"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`DROP TABLE job_aliases`); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(migrations.JobAliases))
	if _, err = db.db.Exec(`DELETE FROM schema_migrations WHERE checksum=$1`, sum[:]); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db.db); err != nil {
		t.Fatal(err)
	}
	for _, posting := range []string{a, b} {
		if closed, err := db.ConfirmClosedURL(ctx, posting, posting); closed || !errors.Is(err, ErrAmbiguousJobIdentity) {
			t.Fatalf("ambiguous closure=%t err=%v", closed, err)
		}
	}
	all, err := db.Jobs(ctx)
	if err != nil || len(all) != 1 || all[0].Closed {
		t.Fatalf("jobs=%+v err=%v", all, err)
	}
}
