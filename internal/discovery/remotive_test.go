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

func TestSearchRetainsAttributionAndLocation(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"jobs":[{"id":42,"title":"Go Engineer","company_name":"Example","url":"https://remotive.com/remote-jobs/software-dev/example-42","candidate_required_location":"Worldwide"}]}`)), Header: make(http.Header)}, nil
	})}
	jobs, err := Search(context.Background(), client, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Source != "Remotive" || jobs[0].Location != "Worldwide" || jobs[0].URL == "" {
		t.Fatalf("jobs %+v", jobs)
	}
}
