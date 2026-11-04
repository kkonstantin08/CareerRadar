package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRejectsFailedAndEmptyPages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{{"blocked", 403, "denied"}, {"no structured jobs", 200, "<html></html>"}} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			_, err := Fetch(context.Background(), client, "https://example.org/jobs")
			if err == nil {
				t.Fatal("expected attention-worthy error")
			}
		})
	}
}

func TestGreenhousePublicAPI(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "boards-api.greenhouse.io" || r.URL.Path != "/v1/boards/example/jobs/123" {
			t.Fatalf("API URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"title":"Go Intern","company_name":"Example","absolute_url":"https://boards.greenhouse.io/example/jobs/123","application_deadline":"2026-12-01","content":"Build APIs"}`)), Header: make(http.Header)}, nil
	})}
	jobs, err := Fetch(context.Background(), client, "https://boards.greenhouse.io/example/jobs/123")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Title != "Go Intern" || jobs[0].Company != "Example" || jobs[0].SourceURL != "https://boards.greenhouse.io/example/jobs/123" {
		t.Fatalf("jobs %+v", jobs)
	}
}

func TestGreenhouseBoardIncludesJobDescriptions(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/boards/example/jobs" || r.URL.Query().Get("content") != "true" {
			t.Fatalf("board request missing content: %s", r.URL)
		}
		body := `{"jobs":[{"title":"Go engineer","absolute_url":"https://boards.greenhouse.io/example/jobs/123","content":"Build services"}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://boards.greenhouse.io/example")
	if err != nil || len(got) != 1 || got[0].Description != "Build services" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestGreenhouseOversizedDescriptionsPreservePartialJobs(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"jobs":[{"title":"Go engineer","absolute_url":"https://boards.greenhouse.io/example/jobs/123"}]}`
		if r.URL.Query().Get("content") == "true" {
			body = strings.Repeat("x", (2<<20)+1)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://boards.greenhouse.io/example")
	if err == nil || len(got) != 1 || got[0].Title != "Go engineer" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestFetchFollowsVacancyLinksAndPagination(t *testing.T) {
	pages := map[string]string{
		"/careers":        `<a href="/jobs/1">Вакансия Go</a><a href="/careers?page=2" rel="next">Next</a>`,
		"/jobs/1":         `<script type="application/ld+json">{"@type":"JobPosting","title":"Go Intern"}</script>`,
		"/careers?page=2": `<script type="application/ld+json">{"@type":"JobPosting","title":"Python Intern"}</script>`,
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body, ok := pages[r.URL.RequestURI()]
		if !ok {
			t.Fatalf("unexpected URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	jobs, err := Fetch(context.Background(), client, "https://example.org/careers")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].Title != "Go Intern" || jobs[1].Title != "Python Intern" {
		t.Fatalf("jobs %+v", jobs)
	}
}

func TestFetchDoesNotMergeMultipleJobsWithoutURLs(t *testing.T) {
	body := `<script type="application/ld+json">[{"@type":"JobPosting","title":"Go"},{"@type":"JobPosting","title":"Python"}]</script>`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	found, err := Fetch(context.Background(), client, "https://example.org/jobs")
	if err == nil || len(found) != 0 {
		t.Fatalf("ambiguous jobs %+v, %v", found, err)
	}
}

func TestFetchResolvesRelativeJobURLs(t *testing.T) {
	body := `<script type="application/ld+json">[{"@type":"JobPosting","title":"Go","url":"/jobs/1"},{"@type":"JobPosting","title":"Python","url":"/jobs/2"}]</script>`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://example.org/careers")
	if err != nil || len(got) != 2 || got[0].URL != "https://example.org/jobs/1" || got[1].URL != "https://example.org/jobs/2" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestFetchRejectsUnsafeRelativeJobURLs(t *testing.T) {
	body := `<script type="application/ld+json">[{"@type":"JobPosting","title":"Bad","url":"javascript:alert(1)"},{"@type":"JobPosting","title":"Good","url":"/jobs/2"}]</script>`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://example.org/careers")
	if err == nil || len(got) != 1 || got[0].URL != "https://example.org/jobs/2" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestJobicyClosedStatusIsExplicitAndBoundToID(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		closed     bool
		wantErr    bool
	}{
		{"closed", `{"success":true,"jobs":[{"id":123,"status":"closed"}]}`, true, false},
		{"unknown", `{"success":true,"jobs":[{"id":123,"status":"unknown"}]}`, false, true},
		{"wrong id", `{"success":true,"jobs":[{"id":124,"status":"closed"}]}`, false, true},
		{"failure", `{"success":false,"jobs":[{"id":123,"status":"closed"}]}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://jobicy.com/api/jobs/status?ids=123" {
					t.Fatalf("unexpected request %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header), Request: r}, nil
			})}
			got, err := Fetch(context.Background(), client, "https://jobicy.com/jobs/123-example")
			if (err != nil) != tc.wantErr || (len(got) == 1 && got[0].Closed) != tc.closed {
				t.Fatalf("jobs %+v error %v", got, err)
			}
		})
	}
}

func TestJobicyActiveStatusReadsVacancy(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Go developer"}</script><a href="/jobs/456-other">Other jobs</a>`
		if r.URL.Path == "/api/jobs/status" {
			body = `{"success":true,"jobs":[{"id":123,"status":"active"}]}`
		} else if r.URL.Path != "/jobs/123-example" {
			t.Fatalf("unexpected related page %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://jobicy.com/jobs/123-example")
	if err != nil || len(got) != 1 || got[0].Title != "Go developer" || got[0].Closed {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestKnownJobicyStatusDoesNotRepeatStatusRequest(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s", r.URL)
		return nil, nil
	})}
	got, err := FetchJobicyStatus(context.Background(), client, "https://jobicy.com/jobs/123-example", "closed")
	if err != nil || len(got) != 1 || !got[0].Closed {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestJobicyBatchStatusRequiresExactResponseIDs(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"success":true,"jobs":[{"id":123,"status":"active"},{"id":456,"status":"closed"}]}`, true},
		{`{"success":true,"jobs":[{"id":123,"status":"active"},{"id":789,"status":"closed"}]}`, false},
		{`{"success":true,"jobs":[{"id":123,"status":"active"},{"id":123,"status":"closed"}]}`, false},
	} {
		client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header), Request: r}, nil
		})}
		statuses, err := JobicyStatuses(context.Background(), client, []string{"123", "456"})
		if (err == nil) != tc.valid || tc.valid && (statuses["123"] != "active" || statuses["456"] != "closed") {
			t.Fatalf("statuses %v error %v", statuses, err)
		}
	}
}

func TestJobicyRateLimitCoversAllProviderURLs(t *testing.T) {
	for _, raw := range []string{"https://jobicy.com/jobs/123-example?utm_source=x", "https://www.jobicy.com/jobs/123", "https://JOBICY.COM:443/jobs/123", "https://jobicy.com/jobs"} {
		if !IsJobicyURL(raw) {
			t.Errorf("provider URL not recognized: %s", raw)
		}
	}
	if IsJobicyURL("https://jobicy.com.attacker.example/jobs/123") {
		t.Fatal("unrelated host classified as Jobicy")
	}
}

func TestJobicyCanonicalJobURLVariants(t *testing.T) {
	for _, raw := range []string{"https://www.jobicy.com/jobs/123-example?utm_source=x#details", "https://JOBICY.COM:443/jobs/123-example/"} {
		if got := JobicyCanonicalURL(raw); got != "https://jobicy.com/jobs/123-example" {
			t.Errorf("canonical URL for %s: %q", raw, got)
		}
	}
	for _, raw := range []string{"https://jobicy.com.attacker.example/jobs/123-example", "https://jobicy.com:444/jobs/123-example", "https://jobicy.com/jobs/123-example?other=1"} {
		if got := JobicyCanonicalURL(raw); got != "" {
			t.Errorf("unsafe or ambiguous URL %s: %q", raw, got)
		}
	}
}

func TestRedirectCannotBypassJobicyThrottle(t *testing.T) {
	client := Client()
	from, _ := http.NewRequest(http.MethodGet, "https://other.example/jobs/123", nil)
	to, _ := http.NewRequest(http.MethodGet, "https://jobicy.com/jobs/123", nil)
	if err := client.CheckRedirect(to, []*http.Request{from}); err == nil {
		t.Fatal("unthrottled redirect into Jobicy accepted")
	}
}
