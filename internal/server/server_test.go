package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/monitor"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"github.com/kkonstantin08/CareerRadar/internal/telegram"
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

func TestJobFiltersAcceptRepeatedValuesAndBoundInput(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/jobs?q=backend&level=Intern&level=Junior&language=Go&limit=50&cursor=100", nil)
	filter, ok := jobFilters(req)
	if !ok || filter.Query != "backend" || len(filter.Levels) != 2 || filter.Levels[1] != "Junior" || len(filter.Languages) != 1 || filter.Languages[0] != "Go" || filter.Limit != 50 || filter.Cursor != 100 {
		t.Fatalf("filter = %+v, valid = %t", filter, ok)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/jobs?q="+strings.Repeat("x", 101), nil)
	if _, ok = jobFilters(req); ok {
		t.Fatal("accepted an oversized search query")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/jobs?q="+url.QueryEscape(strings.Repeat("ю", 100)), nil)
	if _, ok = jobFilters(req); !ok {
		t.Fatal("rejected a 100-character Cyrillic search query")
	}
	defaultPage, ok := jobFilters(httptest.NewRequest(http.MethodGet, "/api/jobs", nil))
	if !ok || defaultPage.Limit != 50 || defaultPage.Cursor != 0 {
		t.Fatalf("default page = %+v, valid = %t", defaultPage, ok)
	}
	for _, query := range []string{"/api/jobs?limit=0", "/api/jobs?limit=101", "/api/jobs?cursor=-1", "/api/jobs?cursor=1&cursor=2"} {
		if _, ok = jobFilters(httptest.NewRequest(http.MethodGet, query, nil)); ok {
			t.Errorf("accepted invalid pagination query %q", query)
		}
	}
}

func TestJobsEndpointAppliesCatalogFilters(t *testing.T) {
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
	base := fmt.Sprintf("https://catalog-api.example.org/filter-test-%d", time.Now().UnixNano())
	company := fmt.Sprintf("Catalog Filter Test %d", time.Now().UnixNano())
	source, err := db.AddSource(ctx, "Catalog API", base, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, source.ID)
	for _, item := range []store.Observation{
		{SourceID: source.ID, URL: base + "/1", Title: "Backend", Company: company, Direction: "Backend", Language: "Python, Go"},
		{SourceID: source.ID, URL: base + "/2", Title: "Backend second", Company: company, Direction: "Backend", Language: "Go"},
		{SourceID: source.ID, URL: base + "/3", Title: "QA", Company: company, Direction: "QA", Language: "Python"},
	} {
		if _, err = db.Observe(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/jobs?company=" + url.QueryEscape(company) + "&direction=Backend&direction=Data+Engineering&language=Go&limit=1"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	var got []struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if w.Code != http.StatusOK || w.Header().Get("X-Has-More") != "true" || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 1 || w.Header().Get("X-Next-Cursor") == "" {
		t.Fatalf("catalog response %d: %s", w.Code, w.Body.String())
	}
	firstURL := got[0].URL
	cursor := w.Header().Get("X-Next-Cursor")
	if _, err = db.Observe(context.Background(), store.Observation{SourceID: source.ID, URL: base + "/4", Title: "New matching row", Company: company, Direction: "Backend", Language: "Go"}); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, path+"&cursor="+cursor, nil)
	req.SetBasicAuth("owner", "secret")
	w = httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	got = nil
	if w.Code != http.StatusOK || w.Header().Get("X-Has-More") != "false" || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 1 || got[0].URL != base+"/1" || got[0].URL == firstURL || w.Header().Get("X-Next-Cursor") == "" {
		t.Fatalf("second catalog page %d: %s", w.Code, w.Body.String())
	}
}

func TestJobsEndpointPaginatesAfterPreferenceFiltering(t *testing.T) {
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
	previous, err := db.Preferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.SavePreferences(context.Background(), previous) }()
	maxHours := 10
	company := fmt.Sprintf("Pagination preference %d", time.Now().UnixNano())
	if err = db.SavePreferences(ctx, jobs.Preferences{MaxHours: &maxHours}); err != nil {
		t.Fatal(err)
	}
	base := "https://catalog-api.example.org/preference-page-" + url.QueryEscape(company)
	source, err := db.AddSource(ctx, "Preference page", base, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DeleteSource(context.Background(), source.ID) })
	tooLong, short := 20, 5
	for _, item := range []store.Observation{
		{SourceID: source.ID, URL: base + "/unsuitable", Title: "Too many hours", Company: company, Hours: &tooLong},
		{SourceID: source.ID, URL: base + "/suitable", Title: "Allowed hours", Company: company, Hours: &short},
	} {
		if _, err = db.Observe(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs?company="+url.QueryEscape(company)+"&limit=1", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	var got []struct {
		URL string `json:"url"`
	}
	if w.Code != http.StatusOK || w.Header().Get("X-Has-More") != "false" || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 1 || got[0].URL != base+"/suitable" {
		t.Fatalf("preference-filtered page %d, more=%s: %s", w.Code, w.Header().Get("X-Has-More"), w.Body.String())
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
	previous, err := db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.SavePreferences(context.Background(), previous) }()
	req := httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(`{"levels":["Intern","Junior"],"languages":["Go"],"max_hours":30,"min_salary":50000,"max_salary":150000,"currency":" RUB "}`))
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
	if !strings.Contains(w.Body.String(), `"max_salary":150000`) || !strings.Contains(w.Body.String(), `"currency":"RUB"`) {
		t.Fatalf("pay preferences were not normalized and persisted: %s", w.Body.String())
	}
	for _, body := range []string{
		`{"max_salary":100000}`,
		`{"min_salary":150000,"max_salary":100000,"currency":"RUB"}`,
	} {
		req = httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(body))
		req.SetBasicAuth("owner", "secret")
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid pay preferences accepted (%s): %d", body, w.Code)
		}
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

func TestCatalogUsesPersistedSalaryRanges(t *testing.T) {
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
	schema := fmt.Sprintf("range_api_%d", time.Now().UnixNano())
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
	src, err := db.AddSource(ctx, "Range", "https://range.example", true)
	if err != nil {
		t.Fatal(err)
	}
	min, max := int64(100000), int64(130000)
	if err = db.SavePreferences(ctx, jobs.Preferences{MinSalary: &min, MaxSalary: &max, Currency: "RUB"}); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		name   string
		lo, hi int64
	}{{"low", 50000, 90000}, {"cross-low", 90000, 120000}, {"within", 110000, 125000}, {"cross-high", 120000, 140000}, {"high", 140000, 150000}} {
		lo, hi := x.lo, x.hi
		if _, err = db.Observe(ctx, store.Observation{SourceID: src.ID, URL: src.URL + "/" + x.name, Title: x.name, SalaryMin: &lo, SalaryMax: &hi, Currency: "RUB"}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	New(db, "owner", "secret").ServeHTTP(w, req)
	var result []struct {
		Title     string `json:"title"`
		Match     string `json:"match"`
		SalaryMin *int64 `json:"salary_min"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	found := map[string]string{}
	for _, j := range result {
		if j.SalaryMin == nil {
			t.Fatal("range missing")
		}
		found[j.Title] = j.Match
	}
	if len(found) != 3 || found["cross-low"] != "needs_clarification" || found["within"] != "suitable" || found["cross-high"] != "needs_clarification" {
		t.Fatalf("catalog %+v", found)
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

func TestDiscoveryWaitsForHabrMutationGate(t *testing.T) {
	checks := monitor.New(nil, nil)
	release, err := checks.LockHabr(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/discovery", strings.NewReader(`{"query":"Go"}`)).WithContext(ctx)
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	Handler(nil, "owner", "secret", checks).ServeHTTP(w, req)
	if w.Code != http.StatusRequestTimeout {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDiscoveryAPIQueryCharacterBoundaries(t *testing.T) {
	h := New(nil, "owner", "secret")
	for _, length := range []int{1, 2, 100, 101} {
		query := strings.Repeat("я", length)
		for _, path := range []string{"/api/discovery", "/api/discovery/company", "POST"} {
			var req *http.Request
			if path == "POST" {
				req = httptest.NewRequest(http.MethodPost, "/api/discovery", strings.NewReader(`{"query":" `+query+` "}`))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(http.MethodGet, path+"?q="+url.QueryEscape(query), nil)
			}
			// Cancellation lets valid inputs reach discovery without accessing external sites.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req = req.WithContext(ctx)
			req.SetBasicAuth("owner", "secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			want := http.StatusBadRequest
			if length >= 2 && length <= 100 {
				want = http.StatusBadGateway
				if path == "POST" {
					want = http.StatusRequestTimeout
				}
			}
			if w.Code != want {
				t.Errorf("%s length %d: status %d, want %d", path, length, w.Code, want)
			}
		}
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
	x := store.Observation{SourceID: src.ID, URL: base + "/job", Title: "First", PageURL: base, Deadline: "2026-12-01"}
	if _, err = db.Observe(ctx, x); err != nil {
		t.Fatal(err)
	}
	x.Title = "Changed"
	x.Deadline = "2026-12-15"
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
	if w.Code != 200 || !strings.Contains(w.Body.String(), "First") || !strings.Contains(w.Body.String(), base) || !strings.Contains(w.Body.String(), "2026-12-01") || !strings.Contains(w.Body.String(), "2026-12-15") {
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
	paths := doc["paths"].(map[string]any)
	job := schemas["Job"].(map[string]any)
	fields := job["properties"].(map[string]any)
	for _, name := range []string{"hours", "salary", "salary_min", "salary_max", "currency", "closed", "apply_url"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
	preferences := schemas["Preferences"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"levels", "directions", "locations", "types", "languages", "companies", "work_modes", "max_hours", "min_salary", "max_salary", "currency"} {
		if _, ok := preferences[name]; !ok {
			t.Errorf("missing preference field %s", name)
		}
	}
	put := paths["/api/preferences"].(map[string]any)["put"].(map[string]any)
	requestSchema := put["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if requestSchema["$ref"] != "#/components/schemas/Preferences" {
		t.Errorf("preferences request schema = %v", requestSchema)
	}
	jobsPath := paths["/api/jobs"].(map[string]any)
	list := jobsPath["get"].(map[string]any)
	parameters := list["parameters"].([]any)
	if len(parameters) != 10 {
		t.Fatalf("catalog query parameters = %d, want 10", len(parameters))
	}
	for _, raw := range parameters[1:8] {
		parameter := raw.(map[string]any)
		schema := parameter["schema"].(map[string]any)
		if schema["maxItems"] != float64(20) {
			t.Errorf("%s maxItems = %v, want 20", parameter["name"], schema["maxItems"])
		}
	}
	limit := parameters[8].(map[string]any)["schema"].(map[string]any)
	if limit["default"] != float64(50) || limit["maximum"] != float64(100) {
		t.Errorf("limit schema = %v", limit)
	}
	cursor := parameters[9].(map[string]any)["schema"].(map[string]any)
	if cursor["minimum"] != float64(1) || cursor["format"] != "int64" {
		t.Errorf("cursor schema = %v", cursor)
	}
	response := list["responses"].(map[string]any)["200"].(map[string]any)
	if _, ok := response["headers"].(map[string]any)["X-Has-More"]; !ok {
		t.Error("paginated response does not document X-Has-More")
	}
	if _, ok := response["headers"].(map[string]any)["X-Next-Cursor"]; !ok {
		t.Error("paginated response does not document X-Next-Cursor")
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

func TestCSVImportPreservesExistingSourceAndRejectsPartialImport(t *testing.T) {
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
	suffix := fmt.Sprint(time.Now().UnixNano())
	existingURL := "https://existing.example.org/" + suffix
	newURL := "https://new.example.org/" + suffix
	existing, err := db.AddSource(ctx, "Original", existingURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSource(ctx, existing.ID)
	h := New(db, "owner", "secret")
	send := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/sources/import", strings.NewReader(body))
		req.SetBasicAuth("owner", "secret")
		req.Header.Set("Content-Type", "text/csv")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if code := send("name,url\nNew," + newURL + "\nInvalid,not-a-url\n"); code != http.StatusBadRequest {
		t.Fatalf("invalid import status=%d", code)
	}
	if code := send("name,url\nReplacement," + existingURL + "\n"); code != http.StatusOK {
		t.Fatalf("duplicate import status=%d", code)
	}
	sources, err := db.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range sources {
		if s.URL == newURL {
			t.Fatal("invalid import partially inserted a source")
		}
		if s.URL == existingURL {
			found = true
			if s.Name != "Original" || !s.Enabled {
				t.Fatalf("existing source changed: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("existing source disappeared")
	}
}

func TestTelegramAndWebShareApplicationStatus(t *testing.T) {
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
	schema := fmt.Sprintf("status_sync_%d", time.Now().UnixNano())
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
	source, err := db.AddSource(ctx, "Example", "https://example.org/careers", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Observe(ctx, store.Observation{SourceID: source.ID, URL: "https://example.org/jobs/1", Title: "Engineer"}); err != nil {
		t.Fatal(err)
	}
	list, err := db.Jobs(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("jobs=%+v err=%v", list, err)
	}
	id := list[0].ID
	code, err := db.NewLinkCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.LinkTelegram(ctx, code, 123); err != nil {
		t.Fatal(err)
	}
	h := New(db, "owner", "secret")
	bot := telegram.New(db)
	if _, err = db.ApplyTelegramStatus(ctx, 100, 123, id, "Откликнулся"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/jobs/%d", id), nil)
	req.SetBasicAuth("owner", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Откликнулся") {
		t.Fatalf("web %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/jobs/%d/status", id), strings.NewReader(`{"status":"Собеседование"}`))
	req.SetBasicAuth("owner", "secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update %d %s", w.Code, w.Body.String())
	}
	reply := bot.Handle(ctx, telegram.Message{ChatID: 123, UserID: 123, ChatType: "private", Text: "/jobs"})
	if !strings.Contains(reply, "Собеседование") {
		t.Fatalf("telegram reply %q", reply)
	}
}
