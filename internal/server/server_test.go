package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/store"
)

func TestAPIRequiresAuthenticationAndCreatesSources(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(db, "owner", "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status: %d", w.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/sources", strings.NewReader(`{"name":"Example","url":"https://example.org/careers"}`))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/sources", strings.NewReader(`{"name":"Local","url":"http://127.0.0.1/admin"}`))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid url: %d %s", w.Code, w.Body.String())
	}
}

func TestMonitoringStoresObservationsAndPreservesThemOnFailure(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, err := db.AddSource(context.Background(), "Monitor test", "https://monitor.example.org/jobs", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateSource(context.Background(), src.ID, "Monitor test", src.URL, true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fetch := func(context.Context, string) ([]collector.Job, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("unavailable")
		}
		return []collector.Job{{Title: "Backend Intern", URL: "https://monitor.example.org/job/1", SourceURL: src.URL}}, nil
	}
	h := newHandler(db, "owner", "secret", fetch)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/sources/%d/check", src.ID), nil)
		req.SetBasicAuth("owner", "secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("monitor: %d %s", w.Code, w.Body.String())
		}
	}
	jobs, err := db.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range jobs {
		if j.URL == "https://monitor.example.org/job/1" {
			found = true
		}
	}
	if !found {
		t.Fatal("job lost")
	}
	sources, err := db.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		if s.ID == src.ID && s.Problem != "unavailable" {
			t.Fatalf("problem = %q", s.Problem)
		}
	}
}

func TestSourceCanBeConfirmedAndEdited(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, err := db.AddSource(context.Background(), "Pending", "https://confirm.example.org/careers", false)
	if err != nil {
		t.Fatal(err)
	}
	h := New(db, "owner", "secret")
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/sources/%d", src.ID), strings.NewReader(`{"name":"Confirmed","url":"https://confirm.example.org/careers","enabled":true}`))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	sources, err := db.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		if s.ID == src.ID {
			if !s.Enabled || s.Name != "Confirmed" {
				t.Fatalf("source %+v", s)
			}
			return
		}
	}
	t.Fatal("source missing")
}

func TestChecksForSameSourceDoNotOverlap(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, err := db.AddSource(context.Background(), "Concurrency", "https://concurrency.example.org", true)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.UpdateSource(context.Background(), src.ID, src.Name, src.URL, true)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	fetch := func(context.Context, string) ([]collector.Job, error) {
		entered <- struct{}{}
		<-release
		return nil, errors.New("unavailable")
	}
	h := newHandler(db, "owner", "secret", fetch)
	call := func() {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/sources/%d/check", src.ID), nil)
		req.SetBasicAuth("owner", "secret")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	done := make(chan struct{}, 2)
	go func() { call(); done <- struct{}{} }()
	<-entered
	go func() { call(); done <- struct{}{} }()
	select {
	case <-entered:
		t.Fatal("checks overlapped")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-done
	<-done
}

func TestApplicationStatusValidation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.AddSource(context.Background(), "Status", "https://status.example.org", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Observe(context.Background(), store.Observation{SourceID: source.ID, URL: "https://status.example.org/1", Title: "Engineer"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := db.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, job := range jobs {
		if job.URL == "https://status.example.org/1" {
			id = job.ID
		}
	}
	if id == 0 {
		t.Fatal("job missing")
	}
	h := New(db, "owner", "secret")
	for _, tc := range []struct {
		status string
		code   int
	}{{"Откликнулся", 200}, {"invalid", 400}} {
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/jobs/%d/status", id), strings.NewReader(fmt.Sprintf(`{"status":%q}`, tc.status)))
		req.SetBasicAuth("owner", "secret")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("status %q: %d %s", tc.status, w.Code, w.Body.String())
		}
	}
}

func TestCSVImportIsIdempotentAndDisabled(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(db, "owner", "secret")
	body := "name,url\nCSV Example,https://csv.example.org/careers\n"
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/sources/import", strings.NewReader(body))
		req.SetBasicAuth("owner", "secret")
		req.Header.Set("Content-Type", "text/csv")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("import: %d %s", w.Code, w.Body.String())
		}
	}
	sources, err := db.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, s := range sources {
		if s.URL == "https://csv.example.org/careers" {
			count++
			if s.Enabled {
				t.Fatal("import activated source")
			}
		}
	}
	if count != 1 {
		t.Fatalf("count %d", count)
	}
}

func TestWebCabinetRequiresAuthentication(t *testing.T) {
	h := New(nil, "owner", "secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("owner", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "CareerRadar") {
		t.Fatalf("page %d %s", w.Code, w.Body.String())
	}
}

func TestTelegramLinkCodeEndpoint(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(db, "owner", "secret")
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/link", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "code") {
		t.Fatalf("link code: %d %s", w.Code, w.Body.String())
	}
}

func TestOpenAPIIsAuthenticatedAndAvailable(t *testing.T) {
	h := New(nil, "owner", "secret")
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"openapi"`) {
		t.Fatalf("openapi %d %s", w.Code, w.Body.String())
	}
}

func TestGlobalSearchRejectsEmptyQuery(t *testing.T) {
	h := New(nil, "owner", "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/discovery?q=x", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestPreferencesAPI(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(db, "owner", "secret")
	req := httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(`{"levels":["Intern","Junior"],"languages":["Go"],"max_hours":30}`))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/preferences", nil)
	req.SetBasicAuth("owner", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Junior") {
		t.Fatalf("get %d %s", w.Code, w.Body.String())
	}
}

func TestCatalogHidesExplicitMismatchButRetainsUnknown(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://filter.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Filter", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	for _, j := range []store.Observation{{SourceID: src.ID, URL: base + "/unknown", Title: "Unknown"}, {SourceID: src.ID, URL: base + "/moscow", Title: "Moscow", Location: "Москва"}} {
		if _, err = db.Observe(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.SavePreferences(ctx, jobs.Preferences{Locations: []string{"Казань"}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, base+"/moscow") || !strings.Contains(body, base+"/unknown") || !strings.Contains(body, "needs_clarification") {
		t.Fatalf("catalog: %s", body)
	}
}

func TestCheckHistoryVisibleInAPI(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, err := db.AddSource(ctx, "History", "https://history.example.org", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RecordCheck(ctx, src.ID, "HTTP 503"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/checks", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "HTTP 503") {
		t.Fatalf("checks %d %s", w.Code, w.Body.String())
	}
}

func TestCompanyDiscoveryValidatesName(t *testing.T) {
	h := New(nil, "owner", "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/discovery/company?q=x", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestPartialCheckRetainsFoundJobsAndWarning(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://partial.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Partial", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	fetch := func(context.Context, string) ([]collector.Job, error) {
		return []collector.Job{{Title: "Partial result", URL: base + "/job", SourceURL: base}}, errors.New("page unavailable")
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/sources/%d/check", src.ID), nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	newHandler(db, "owner", "secret", fetch).ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "page unavailable") {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	xs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		if x.URL == base+"/job" {
			return
		}
	}
	t.Fatal("partial result lost")
}

func TestJobDetailsContainHistoryAndEvidence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://details.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Details", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	x := store.Observation{SourceID: src.ID, URL: base + "/job", Title: "First", PageURL: base}
	if _, err = db.Observe(ctx, x); err != nil {
		t.Fatal(err)
	}
	x.Title = "Changed"
	if _, err = db.Observe(ctx, x); err != nil {
		t.Fatal(err)
	}
	xs, err := db.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, j := range xs {
		if j.URL == x.URL {
			id = j.ID
		}
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/jobs/%d", id), nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "First") || !strings.Contains(w.Body.String(), base) {
		t.Fatalf("details %d %s", w.Code, w.Body.String())
	}
}

func TestImportSourcesSheetCSV(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	unique := fmt.Sprintf("https://sheet.example.org/%d", time.Now().UnixNano())
	csv := fmt.Sprintf("source_id,enabled,source_class,company,label,start_url,notes\nx,TRUE,DIRECT,Example,Internships,%s,notes\n", unique)
	req := httptest.NewRequest(http.MethodPost, "/api/sources/import", strings.NewReader(csv))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "text/csv")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("import %d %s", w.Code, w.Body.String())
	}
	list, err := db.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.URL == unique {
			defer db.DeleteSource(context.Background(), s.ID)
			if s.Enabled || s.Name != "Example" {
				t.Fatalf("source %+v", s)
			}
			return
		}
	}
	t.Fatal("source missing")
}

func TestCatalogFiltersExplicitHoursAndSalary(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://hoursfilter.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Hours filter", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	hours := 40
	salary := int64(70000)
	_, err = db.Observe(ctx, store.Observation{SourceID: src.ID, URL: base + "/job", Title: "Engineer", Hours: &hours, Salary: &salary, Currency: "RUB"})
	if err != nil {
		t.Fatal(err)
	}
	max := 30
	if err = db.SavePreferences(ctx, jobs.Preferences{MaxHours: &max}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), base+"/job") {
		t.Fatalf("catalog %d %s", w.Code, w.Body.String())
	}
}

func TestOwnerConfirmsClosureWithEvidence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := fmt.Sprintf("https://close-api.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Close", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	if _, err = db.Observe(ctx, store.Observation{SourceID: src.ID, URL: base + "/job", Title: "Intern"}); err != nil {
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
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/jobs/%d/close", id), strings.NewReader(fmt.Sprintf(`{"evidence":%q}`, base+"/job")))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("close %d %s", w.Code, w.Body.String())
	}
}

func TestOpenAPIDocumentsJobFields(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(openAPI, &doc); err != nil {
		t.Fatal(err)
	}
	components := doc["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	job := schemas["Job"].(map[string]any)
	fields := job["properties"].(map[string]any)
	for _, name := range []string{"hours", "salary", "currency", "closed"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
}

func TestUncertainNotificationResolutionAPI(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fmt.Sprintf("api-notice-%d", time.Now().UnixNano())
	if err = db.QueueNotification(ctx, key, "inspect notification"); err != nil {
		t.Fatal(err)
	}
	_, err = db.ClaimNotification(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	defer raw.ExecContext(ctx, `DELETE FROM notifications WHERE event_key=$1`, key)
	var id int64
	if err = raw.QueryRowContext(ctx, `UPDATE notifications SET state='unknown' WHERE event_key=$1 RETURNING id`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	h := New(db, "owner", "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/uncertain", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "inspect notification") {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/notifications/%d/resolve", id), strings.NewReader(`{"decision":"sent"}`))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("resolve %d %s", w.Code, w.Body.String())
	}
}

func TestCatalogFiltersExplicitDirectionAndLanguage(t *testing.T) {
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
	base := fmt.Sprintf("https://skills-api.example.org/%d", time.Now().UnixNano())
	src, err := db.AddSource(ctx, "Skills", base, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, src.ID)
	for _, x := range []store.Observation{{SourceID: src.ID, URL: base + "/go", Direction: "Backend", Language: "Go"}, {SourceID: src.ID, URL: base + "/java", Direction: "Backend", Language: "Java"}} {
		if _, err = db.Observe(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.SavePreferences(ctx, jobs.Preferences{Directions: []string{"Backend"}, Languages: []string{"Go"}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), base+"/go") || strings.Contains(w.Body.String(), base+"/java") {
		t.Fatalf("catalog %d %s", w.Code, w.Body.String())
	}
}
