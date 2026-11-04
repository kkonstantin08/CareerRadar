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
