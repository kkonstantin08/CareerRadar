package discovery

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDiscoveryQueryLimitsCountCharacters(t *testing.T) {
	for _, length := range []int{1, 2, 100, 101} {
		query := strings.Repeat("я", length)
		for _, provider := range []string{"jobs", "company"} {
			calls := 0
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"search":[],"jobs":[]}`)), Header: make(http.Header)}, nil
			})}
			var err error
			if provider == "jobs" {
				_, err = Search(context.Background(), client, query)
			} else {
				_, err = Company(context.Background(), client, query)
			}
			valid := length >= 2 && length <= 100
			if (err == nil) != valid || (calls > 0) != valid {
				t.Errorf("%s length %d: calls=%d err=%v", provider, length, calls, err)
			}
		}
	}
}

func TestSearchRetainsAttributionAndLocation(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"jobs":[{"id":42,"title":"Go Engineer","company_name":"Example","url":"https://remotive.com/remote-jobs/software-dev/example-42","candidate_required_location":"Worldwide","description":"<p>Build APIs</p><p>Go required</p>"}]}`)), Header: make(http.Header)}, nil
	})}
	jobs, err := Search(context.Background(), client, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Source != "Remotive" || jobs[0].Location != "Worldwide" || jobs[0].URL == "" {
		t.Fatalf("jobs %+v", jobs)
	}
	if !strings.Contains(jobs[0].Description, "Build APIs") || !strings.Contains(jobs[0].Description, "Go required") || strings.Contains(jobs[0].Description, "<p>") {
		t.Fatalf("description %q", jobs[0].Description)
	}
}

func TestDescriptionOmitsScriptsAndStyles(t *testing.T) {
	got := plainDescription(`<p>Requirements: Go</p><script>untrusted()</script><style>.hidden{}</style><p>Remote</p>`)
	if !strings.Contains(got, "Requirements: Go") || !strings.Contains(got, "Remote") || strings.Contains(got, "untrusted") || strings.Contains(got, "hidden") {
		t.Fatalf("description %q", got)
	}
}
