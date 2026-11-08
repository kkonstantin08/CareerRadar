package collector

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kkonstantin08/CareerRadar/internal/jobs"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHabrIndividualCardDoesNotCollectRelatedVacancies(t *testing.T) {
	body, err := os.ReadFile("testdata/habr-job.html")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(string(body), "</body>", `<a href="/vacancies/1000000002">Похожая вакансия Go Developer</a></body>`, 1)
	calls := 0
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(page)), Header: make(http.Header)}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://career.habr.com/vacancies/1000000001")
	if err != nil || len(got) != 1 || calls != 1 {
		t.Fatalf("jobs=%d requests=%d err=%v", len(got), calls, err)
	}
}

func TestHabrBoardStillDiscoversIndividualCards(t *testing.T) {
	body, err := os.ReadFile("testdata/habr-job.html")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		page := string(body)
		if r.URL.Path == "/vacancies" {
			page = `<a href="/vacancies/1000000001">Go Developer vacancy</a>`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(page)), Header: make(http.Header)}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://career.habr.com/vacancies")
	if err != nil || len(got) != 1 || calls != 2 {
		t.Fatalf("jobs=%d requests=%d err=%v", len(got), calls, err)
	}
}

func TestHabrIndividualCardNeverFallsThroughToRelatedJobs(t *testing.T) {
	for _, tc := range []struct {
		body    string
		count   int
		problem bool
	}{
		{`<a href="/vacancies/1000000002">Go vacancy</a>`, 0, true},
		{`<script type="application/ld+json">[{"@type":"JobPosting","title":"Target","url":"/vacancies/1000000001"},{"@type":"JobPosting","title":"Related","url":"/vacancies/1000000002"}]</script><a href="/vacancies/1000000002">Go vacancy</a>`, 1, false},
		{`<script type="application/ld+json">[{"@type":"JobPosting","title":"One"},{"@type":"JobPosting","title":"Two"}]</script><a href="/vacancies/1000000002">Go vacancy</a>`, 0, true},
	} {
		calls := 0
		client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		got, err := Fetch(context.Background(), client, "https://career.habr.com/vacancies/1000000001")
		if calls != 1 || len(got) != tc.count || (err != nil) != tc.problem {
			t.Errorf("requests=%d jobs=%+v err=%v", calls, got, err)
		}
	}
}

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

func TestParseLeverPublicFeed(t *testing.T) {
	f, err := os.Open("testdata/lever-jobs.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseLever(body, "https://jobs.lever.co/examplecompany", "examplecompany", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("jobs %+v, err %v", got, err)
	}
	first := got[0]
	if first.Title != "Go Intern" || first.Company != "Example Company" || first.Location != "Казань" || first.Type != "Internship" || first.Level != "Intern" || first.Description != "Build backend services Learn Go" || first.URL != "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e" || first.ApplyURL != "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e/apply?lever-source=career-radar" {
		t.Fatalf("first Lever listing %+v", first)
	}
	if got[1].Type != "Job" || got[1].Company != "Example Company" || got[1].Location != "Россия" || got[1].WorkMode != "remote" {
		t.Fatalf("second Lever listing %+v", got[1])
	}
}

func TestLeverLocationsMatchInitialGeographyFilters(t *testing.T) {
	body, err := os.ReadFile("testdata/lever-jobs.xml")
	if err != nil {
		t.Fatal(err)
	}
	listings, err := parseLever(body, "https://jobs.lever.co/examplecompany", "examplecompany", "")
	if err != nil || len(listings) != 2 {
		t.Fatalf("Lever listings %+v, err %v", listings, err)
	}
	prefs := jobs.Preferences{Locations: []string{"Россия", "Казань", "Иннополис"}}
	for _, listing := range listings {
		if got := prefs.Match(jobs.Job{Location: listing.Location, WorkMode: listing.WorkMode}); got != jobs.Suitable {
			t.Errorf("location %q mode %q matched as %s", listing.Location, listing.WorkMode, got)
		}
	}
}

func TestLeverWorkModeLeavesConflictingMarkersUnknown(t *testing.T) {
	for _, value := range []string{"Remote / Office", "Удалённо — гибрид"} {
		if got := leverWorkMode(value); got != "" {
			t.Errorf("conflicting mode %q classified as %q", value, got)
		}
	}
}

func TestParseLeverFeedCanSelectOneObservedPosting(t *testing.T) {
	body, err := os.ReadFile("testdata/lever-jobs.xml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseLever(body, "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e", "examplecompany", "4f816f9a-7f03-4e18-9710-6ec9a9c0d40e")
	if err != nil || len(got) != 1 || got[0].Title != "Go Intern" {
		t.Fatalf("selected Lever listing %+v, err %v", got, err)
	}
	missing, err := parseLever(body, "https://jobs.lever.co/examplecompany/00000000-0000-0000-0000-000000000000", "examplecompany", "00000000-0000-0000-0000-000000000000")
	if err == nil || len(missing) != 0 {
		t.Fatalf("missing listing treated as closed: %+v, err %v", missing, err)
	}
}

func TestParseLeverRejectsAnUntrustedApplyURLAndRetainsThePosting(t *testing.T) {
	body, err := os.ReadFile("testdata/lever-jobs.xml")
	if err != nil {
		t.Fatal(err)
	}
	unsafe := strings.Replace(string(body), "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e/apply?lever-source=career-radar", "https://attacker.example/apply", 1)
	got, err := parseLever([]byte(unsafe), "https://jobs.lever.co/examplecompany", "examplecompany", "")
	if err == nil || len(got) != 2 || got[0].ApplyURL != "" || got[0].Title != "Go Intern" {
		t.Fatalf("untrusted Lever apply link was accepted or posting was lost: %+v, err %v", got, err)
	}
}

func TestLeverFetchUsesPublicXMLPostingsFeed(t *testing.T) {
	feed, err := os.ReadFile("testdata/lever-jobs.xml")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.lever.co/v0/postings/examplecompany?mode=xml" {
			t.Fatalf("Lever feed URL %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(feed))), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://jobs.lever.co/examplecompany")
	if err != nil || len(got) != 2 || got[0].Company != "Example Company" || got[0].ApplyURL == "" {
		t.Fatalf("Lever opportunities %+v, err %v", got, err)
	}
	got, err = Fetch(context.Background(), client, "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e")
	if err != nil || len(got) != 1 || got[0].Title != "Go Intern" {
		t.Fatalf("individual Lever opportunity %+v, err %v", got, err)
	}
}

func TestLeverExactUnredirectedNotFoundConfirmsClosure(t *testing.T) {
	const jobURL = "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e"
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://api.lever.co/v0/postings/examplecompany?mode=xml" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("<postings></postings>")), Header: make(http.Header), Request: r}, nil
		}
		if r.URL.String() != jobURL {
			t.Fatalf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, jobURL)
	if err != nil || len(got) != 1 || !got[0].Closed || got[0].URL != jobURL || got[0].SourceURL != jobURL {
		t.Fatalf("Lever closure evidence %+v, err %v", got, err)
	}
}

func TestLeverRedirectOrForbiddenPageDoesNotConfirmClosure(t *testing.T) {
	const jobURL = "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e"
	for _, tc := range []struct {
		name   string
		status int
		check  func(*http.Request) bool
	}{
		{"forbidden", http.StatusForbidden, nil},
		{"redirect", http.StatusFound, func(r *http.Request) bool { return r.URL.String() == jobURL }},
		{"feed omission only", http.StatusOK, func(r *http.Request) bool { return r.URL.String() == jobURL }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.String(), "https://api.lever.co/") {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("<postings></postings>")), Header: make(http.Header), Request: r}, nil
				}
				if tc.check != nil && !tc.check(r) {
					t.Fatalf("unexpected request %s", r.URL)
				}
				header := make(http.Header)
				if tc.status == http.StatusFound {
					header.Set("Location", "https://jobs.lever.co/examplecompany/other")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("<html><h1>Go role</h1></html>")), Header: header, Request: r}, nil
			}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			got, err := Fetch(context.Background(), client, jobURL)
			if err == nil || len(got) != 0 {
				t.Fatalf("non-404 response confirmed closure: %+v, err %v", got, err)
			}
		})
	}
}

func TestLeverRedirectChainReturningToCanonicalDoesNotConfirmClosure(t *testing.T) {
	const jobURL = "https://jobs.lever.co/examplecompany/4f816f9a-7f03-4e18-9710-6ec9a9c0d40e"
	const alias = "https://jobs.lever.co/examplecompany/alias"
	canonicalRequests := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.lever.co/v0/postings/examplecompany?mode=xml":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("<postings></postings>")), Header: make(http.Header), Request: r}, nil
		case jobURL:
			canonicalRequests++
			if canonicalRequests > 1 {
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
			}
			header := make(http.Header)
			header.Set("Location", alias)
			return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")), Header: header, Request: r}, nil
		case alias:
			header := make(http.Header)
			header.Set("Location", jobURL)
			return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")), Header: header, Request: r}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	})}
	got, err := Fetch(context.Background(), client, jobURL)
	if err == nil || len(got) != 0 {
		t.Fatalf("redirect chain confirmed closure: %+v, err %v", got, err)
	}
}

func TestFetchRetainsExplicitHTMLJobTitleWhenStructuredDataIsMissing(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `<html><main><h1>Go Intern</h1><a href="/apply/123">Apply now</a></main></html>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://careers.example/careers/jobs/go-intern")
	if err == nil || len(got) != 1 || got[0].Title != "Go Intern" || got[0].URL != "https://careers.example/careers/jobs/go-intern" || got[0].ApplyURL != "https://careers.example/apply/123" || got[0].Company != "" || got[0].Deadline != "" {
		t.Fatalf("partial HTML job %+v error %v", got, err)
	}
}

func TestFetchRetainsMicrodataJobsWhenItemrefWorkLimitIsReached(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<html><body><div id="shared">`)
	body.WriteString(strings.Repeat(`<i>x</i>`, 30000))
	body.WriteString(`</div><article itemscope itemtype="https://schema.org/JobPosting" itemref="shared"><h1 itemprop="title">Go Engineer One</h1><a itemprop="url" href="https://careers.example/jobs/one">Details</a></article>`)
	body.WriteString(`<article itemscope itemtype="https://schema.org/JobPosting" itemref="shared"><h1 itemprop="title">Go Engineer Two</h1><a itemprop="url" href="https://careers.example/jobs/two">Details</a></article></body></html>`)
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/jobs" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body.String())), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://careers.example/jobs")
	if err == nil || len(got) != 2 || got[0].Title != "Go Engineer One" || got[1].Title != "Go Engineer Two" {
		t.Fatalf("fetch lost partial opportunities at itemref limit: %+v, err %v", got, err)
	}
}

func TestFetchRetainsEducationalProgramAndApplyLink(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `<article itemscope itemtype="https://schema.org/EducationalOccupationalProgram"><h1 itemprop="name">Go Bootcamp</h1><span itemprop="provider" itemscope itemtype="https://schema.org/Organization"><span itemprop="name">Example Academy</span></span><a href="/apply">Apply now</a></article>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://careers.example/programs/go")
	if err != nil || len(got) != 1 || got[0].Type != "Education / Bootcamp" || got[0].Title != "Go Bootcamp" || got[0].Company != "Example Academy" || got[0].ApplyURL != "https://careers.example/apply" {
		t.Fatalf("program %+v, err %v", got, err)
	}
}

func TestGreenhousePublicAPI(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "boards-api.greenhouse.io" || r.URL.Path != "/v1/boards/example/jobs/123" {
			t.Fatalf("API URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"title":"Go Intern","company_name":"Example","absolute_url":"https://boards.greenhouse.io/example/jobs/123","application_deadline":"2026-12-01","location":{"name":"Казань"},"content":"Build APIs"}`)), Header: make(http.Header)}, nil
	})}
	jobs, err := Fetch(context.Background(), client, "https://boards.greenhouse.io/example/jobs/123")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Title != "Go Intern" || jobs[0].Company != "Example" || jobs[0].SourceURL != "https://boards.greenhouse.io/example/jobs/123" || jobs[0].Location != "Казань" || jobs[0].Type != "Internship" || jobs[0].Level != "Intern" {
		t.Fatalf("jobs %+v", jobs)
	}
}

func TestGreenhouseJobNotFoundIsExplicitClosureSignal(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://boards-api.greenhouse.io/v1/boards/example/jobs/123" {
			t.Fatalf("API URL %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://boards.greenhouse.io/example/jobs/123")
	if err != nil || len(got) != 1 || !got[0].Closed || got[0].URL != "https://boards.greenhouse.io/example/jobs/123" || got[0].SourceURL != "https://boards-api.greenhouse.io/v1/boards/example/jobs/123" {
		t.Fatalf("closure evidence %+v error %v", got, err)
	}
}

func TestGreenhouseBoardNotFoundDoesNotConfirmClosure(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
	})}
	if got, err := Fetch(context.Background(), client, "https://boards.greenhouse.io/example"); err == nil || len(got) != 0 {
		t.Fatalf("board failure treated as closure: %+v %v", got, err)
	}
}

func TestGreenhouseRedirectedNotFoundDoesNotConfirmClosure(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "boards-api.greenhouse.io" {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://other.example/missing"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: r}, nil
	})}
	if got, err := Fetch(context.Background(), client, "https://boards.greenhouse.io/example/jobs/123"); err == nil || len(got) != 0 {
		t.Fatalf("redirected 404 treated as closure: %+v %v", got, err)
	}
}

func TestGreenhouseClosureEvidenceAcceptsThePublishedJobTrackingLink(t *testing.T) {
	jobURL := "https://boards.greenhouse.io/example/jobs/123?gh_jid=123&utm_source=career#apply"
	want := "https://boards-api.greenhouse.io/v1/boards/example/jobs/123"
	if got := GreenhouseClosureEvidenceURL(jobURL); got != want {
		t.Fatalf("evidence %q, want %q", got, want)
	}
	if got := GreenhouseCanonicalURL(jobURL); got != "https://boards.greenhouse.io/example/jobs/123?gh_jid=123" {
		t.Fatalf("canonical URL %q", got)
	}
	if got := GreenhouseClosureEvidenceURL("https://boards.greenhouse.io/example/jobs/123?gh_jid=124"); got != "" {
		t.Fatalf("mismatched gh_jid accepted: %q", got)
	}
}

func TestAshbyPublicBoard(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.ashbyhq.com/posting-api/job-board/example?includeCompensation=true" {
			t.Fatalf("URL %s", r.URL)
		}
		body := `{"apiVersion":"1","jobs":[{"title":"Junior Go Engineer","isListed":true,"workplaceType":"Remote","employmentType":"Intern","descriptionPlain":"Build services","jobUrl":"https://jobs.ashbyhq.com/example/abc","applyUrl":"https://jobs.ashbyhq.com/example/abc/application","compensation":{"summaryComponents":[{"compensationType":"EquityPercentage","interval":"NONE"},{"compensationType":"Salary","interval":"1 YEAR","currencyCode":"USD","minValue":60000,"maxValue":84000},{"compensationType":"Bonus","interval":"1 YEAR"}]}},{"title":"Unlisted","isListed":false,"jobUrl":"https://jobs.ashbyhq.com/example/hidden"}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://jobs.ashbyhq.com/example")
	if err != nil || len(got) != 1 || got[0].Title != "Junior Go Engineer" || got[0].WorkMode != "remote" || got[0].Type != "Internship" || got[0].Description != "Build services" || got[0].URL != "https://jobs.ashbyhq.com/example/abc" || got[0].ApplyURL != "https://jobs.ashbyhq.com/example/abc/application" || got[0].SalaryMin == nil || *got[0].SalaryMin != 5000 || got[0].SalaryMax == nil || *got[0].SalaryMax != 7000 || got[0].Currency != "USD" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestAshbyDoesNotGuessAmbiguousOrHourlyCompensation(t *testing.T) {
	for _, summary := range []string{
		`[{"compensationType":"Salary","interval":"1 YEAR","currencyCode":"USD","minValue":60000,"maxValue":90000},{"compensationType":"Salary","interval":"1 YEAR","currencyCode":"USD","minValue":80000,"maxValue":110000}]`,
		`[{"compensationType":"Salary","interval":"1 HOUR","currencyCode":"USD","minValue":50,"maxValue":80}]`,
	} {
		body := `{"jobs":[{"title":"Engineer","isListed":true,"workplaceType":"Remote","jobUrl":"https://jobs.ashbyhq.com/example/1","compensation":{"summaryComponents":` + summary + `}}]}`
		got, err := parseAshby([]byte(body), "https://jobs.ashbyhq.com/example")
		if err != nil || len(got) != 1 || got[0].Salary != nil || got[0].SalaryMin != nil || got[0].SalaryMax != nil || got[0].Currency != "" {
			t.Errorf("ambiguous or hourly pay retained: %+v %v", got, err)
		}
	}
}

func TestAshbyIncompleteListingsPreserveValidJobs(t *testing.T) {
	for _, tc := range []struct {
		body  string
		count int
	}{
		{`{"jobs":[{"title":"Go","isListed":true,"jobUrl":"https://jobs.ashbyhq.com/example/1"},{"title":"Unknown","jobUrl":"https://jobs.ashbyhq.com/example/2"}]}`, 1},
		{`{"jobs":[]}`, 0},
		{`{"error":"unavailable"}`, 0},
	} {
		got, err := parseAshby([]byte(tc.body), "https://jobs.ashbyhq.com/example")
		if err == nil || len(got) != tc.count {
			t.Fatalf("jobs %+v error %v", got, err)
		}
	}
}

func TestAshbyAnnualSalaryKeepsFractionalMonthlyBounds(t *testing.T) {
	for _, bounds := range []string{`"minValue":120001,"maxValue":120001`, `"maxValue":120001`, `"minValue":120001`} {
		body := `{"jobs":[{"title":"Engineer","isListed":true,"jobUrl":"https://jobs.ashbyhq.com/example/1","compensation":{"summaryComponents":[{"compensationType":"Salary","interval":"1 YEAR","currencyCode":"RUB",` + bounds + `}]}}]}`
		got, err := parseAshby([]byte(body), "https://jobs.ashbyhq.com/example")
		if err != nil || len(got) != 1 {
			t.Fatalf("jobs %+v, err %v", got, err)
		}
		job := got[0]
		if job.Salary != nil || strings.Contains(bounds, "minValue") && (job.SalaryMin == nil || *job.SalaryMin != 10000) || strings.Contains(bounds, "maxValue") && (job.SalaryMax == nil || *job.SalaryMax != 10001) {
			t.Errorf("rounded annual bounds %s: %+v", bounds, job)
		}
	}
}

func TestAshbyRejectsUnsafeApplyURLWithoutLosingJob(t *testing.T) {
	body := `{"jobs":[{"title":"Engineer","isListed":true,"jobUrl":"https://jobs.ashbyhq.com/example/1","applyUrl":"javascript:alert(1)"}]}`
	got, err := parseAshby([]byte(body), "https://jobs.ashbyhq.com/example")
	if err == nil || len(got) != 1 || got[0].ApplyURL != "" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestAshbyLocationDoesNotGuessRemoteOrMultipleLocations(t *testing.T) {
	body := `{"jobs":[
		{"title":"Office","isListed":true,"workplaceType":"OnSite","secondaryLocations":[],"address":{"postalAddress":{"addressLocality":"Казань"}},"jobUrl":"https://jobs.ashbyhq.com/example/1"},
		{"title":"Remote","isListed":true,"workplaceType":"Remote","address":{"postalAddress":{"addressLocality":"Москва"}},"jobUrl":"https://jobs.ashbyhq.com/example/2"},
		{"title":"Multiple","isListed":true,"workplaceType":"Hybrid","address":{"postalAddress":{"addressLocality":"Москва"}},"secondaryLocations":[{"location":"Казань"}],"jobUrl":"https://jobs.ashbyhq.com/example/3"},
		{"title":"Unknown","isListed":true,"workplaceType":"OnSite","address":{"postalAddress":{"addressLocality":"Москва"}},"jobUrl":"https://jobs.ashbyhq.com/example/4"}]}`
	got, err := parseAshby([]byte(body), "https://jobs.ashbyhq.com/example")
	if err != nil || len(got) != 4 || got[0].Location != "Казань" || got[1].Location != "" || got[2].Location != "" || !got[2].LocationUncertain || got[3].Location != "" || got[3].LocationUncertain {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestAshbySupportsBoundedLargerBoard(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"jobs":[{"title":"Engineer","isListed":true,"descriptionPlain":"` + strings.Repeat("x", (2<<20)+1) + `","jobUrl":"https://jobs.ashbyhq.com/example/1"}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://jobs.ashbyhq.com/example")
	if err != nil || len(got) != 1 {
		t.Fatalf("jobs %d error %v", len(got), err)
	}
}

func TestAshbyRejectsResponseBeyondLimit(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (8<<20)+1))), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://jobs.ashbyhq.com/example")
	if err == nil || len(got) != 0 {
		t.Fatalf("jobs %d error %v", len(got), err)
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

func TestFetchExtractsUnambiguousApplicationLinkOnJobPage(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Go Engineer"}</script><a href="/applications/42">Откликнуться</a>`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://example.org/jobs/42")
	if err != nil || len(got) != 1 || got[0].ApplyURL != "https://example.org/applications/42" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestFetchDoesNotGuessAmbiguousApplicationLink(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Go Engineer"}</script><a href="/apply/1">Apply now</a><a href="/apply/2">Apply now</a>`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://example.org/jobs/42")
	if err == nil || len(got) != 1 || got[0].ApplyURL != "" {
		t.Fatalf("jobs %+v error %v", got, err)
	}
}

func TestFetchDoesNotUseUnsafeApplicationLink(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Go Engineer"}</script><a href="javascript:alert(1)">Apply now</a>`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://example.org/jobs/42")
	if err == nil || len(got) != 1 || got[0].ApplyURL != "" {
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

func TestFetchWarnsWhenOneLinkedJobPageIsEmpty(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `<a href="/jobs/good">Good job</a><a href="/jobs/empty">Empty job</a>`
		if r.URL.Path == "/jobs/good" {
			body = `<script type="application/ld+json">{"@type":"JobPosting","title":"Go Engineer"}</script>`
		}
		if r.URL.Path == "/jobs/empty" {
			body = `<html><body></body></html>`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	got, err := Fetch(context.Background(), client, "https://example.org/careers")
	if len(got) != 1 || got[0].Title != "Go Engineer" || err == nil {
		t.Fatalf("jobs=%+v err=%v", got, err)
	}
}

func TestFetchDoesNotTreatEmptySearchPageAsJob(t *testing.T) {
	for _, path := range []string{"/jobs/search", "/jobs/search/results", "/jobs/archive", "/jobs/archives", "/jobs/list", "/jobs/all"} {
		t.Run(path, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				body := `<a href="/jobs/good">Good job</a><a href="` + path + `">Search jobs</a>`
				if r.URL.Path == "/jobs/good" {
					body = `<script type="application/ld+json">{"@type":"JobPosting","title":"Go Engineer"}</script>`
				}
				if r.URL.Path == path {
					body = `<html><body></body></html>`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			got, err := Fetch(context.Background(), client, "https://example.org/careers")
			if len(got) != 1 || err != nil {
				t.Fatalf("jobs=%+v err=%v", got, err)
			}
		})
	}
}
