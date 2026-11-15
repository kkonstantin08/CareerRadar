package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSmartRecruitersBoardPaginatesAndRetainsVerifiedJobsOnPartialFailure(t *testing.T) {
	var calls []string
	const detail = `{"id":"101","name":"Go Engineer","experienceLevel":{"id":"internship"},"active":true,"company":{"identifier":"Example","name":"Example Engineering"},"location":{"city":"Казань","remote":true},"postingUrl":"https://jobs.smartrecruiters.com/Example/101-go-intern","applyUrl":"https://jobs.smartrecruiters.com/Example/101-go-intern?oga=true","jobAd":{"sections":{"jobDescription":{"text":"<p>Build services</p>"},"qualifications":{"text":"<p>Know Go</p>"}}}}`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.RequestURI())
		status, body := 200, ""
		switch r.URL.RequestURI() {
		case "/v1/companies/Example/postings?destination=PUBLIC&limit=5&offset=0":
			body = `{"totalFound":2,"offset":0,"content":[{"id":"101"}]}`
		case "/v1/companies/Example/postings?destination=PUBLIC&limit=5&offset=1":
			body = `{"totalFound":2,"offset":1,"content":[{"id":"102","visibility":"PUBLIC"}]}`
		case "/v1/companies/Example/postings/101":
			body = detail
		case "/v1/companies/Example/postings/102":
			status = 403
		default:
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	jobs, err := Fetch(context.Background(), client, "https://careers.smartrecruiters.com/Example")
	if err == nil || len(jobs) != 1 || len(calls) != 4 {
		t.Fatalf("jobs=%+v requests=%v error=%v", jobs, calls, err)
	}
	j := jobs[0]
	if j.Closed || j.Title != "Go Engineer" || j.Type != "Internship" || j.Level != "Intern" || j.Company != "Example Engineering" || j.Location != "Казань" || j.WorkMode != "remote" || !strings.Contains(j.Description, "Know Go") || !strings.HasSuffix(j.ApplyURL, "?oga=true") || j.SourceURL != "https://api.smartrecruiters.com/v1/companies/Example/postings/101" {
		t.Fatalf("incorrect observation: %+v", j)
	}
}

func TestSmartRecruitersUnavailablePostingDoesNotConfirmClosure(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://jobs.smartrecruiters.com/Example/101-go-intern")
	if err == nil || len(got) != 0 {
		t.Fatalf("jobs=%+v error=%v", got, err)
	}
}

func TestSmartRecruitersRejectsMismatchedPostingAndNonPublicDetail(t *testing.T) {
	for _, tc := range []struct {
		name, visibility, postingURL, applyURL string
		active, keep                           bool
	}{
		{"other posting", "PUBLIC", "https://jobs.smartrecruiters.com/Example/102-other", "", true, false},
		{"other employer", "PUBLIC", "https://jobs.smartrecruiters.com/Another/101-test", "", true, false},
		{"internal", "INTERNAL", "https://jobs.smartrecruiters.com/Example/101-test", "", true, false},
		{"inactive", "PUBLIC", "https://jobs.smartrecruiters.com/Example/101-test", "", false, false},
		{"unverified apply", "PUBLIC", "https://jobs.smartrecruiters.com/Example/101-test", "https://jobs.smartrecruiters.com/Example/102-other", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				body := fmt.Sprintf(`{"id":"101","name":"Engineer","active":%t,"visibility":%q,"company":{"identifier":"Example"},"postingUrl":%q,"applyUrl":%q}`, tc.active, tc.visibility, tc.postingURL, tc.applyURL)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
			})}
			got, err := Fetch(context.Background(), client, "https://jobs.smartrecruiters.com/Example/101-test")
			if err == nil || (len(got) == 1) != tc.keep || tc.keep && (got[0].ApplyURL != "" || got[0].Closed) {
				t.Fatalf("jobs=%+v error=%v", got, err)
			}
		})
	}
}

func TestSmartRecruitersPostingIdentityIgnoresTitleSlug(t *testing.T) {
	old := PostingURL("https://jobs.smartrecruiters.com/Example/101-old-title")
	current := PostingURL("https://jobs.smartrecruiters.com/Example/101-new-title?oga=true")
	if old == "" || old != current || current == PostingURL("https://jobs.smartrecruiters.com/Other/101-new-title") {
		t.Fatalf("identities: old=%q current=%q", old, current)
	}
}
