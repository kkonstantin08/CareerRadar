package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFutureTodayUsesPublicInternshipState(t *testing.T) {
	const board = "https://it.fut.ru/internship"
	const card = board + "/acme.example/summer"
	const catalog = `{"query":{"alias_company_or_type":"internship"},"props":{"pageProps":{"data":{
  "publications":[[],[
    {"title":"Go Intern","description":"Learn Go","company":{"alias":"acme.example","caption":"Acme"},"alias":"summer","generatedLink":"/internship/acme.example/summer?utm_source=fut","publication_type":{"alias":"internship"},"event_status":"published"},
    {"title":"Waitlist","company":{"alias":"acme.example","caption":"Acme"},"alias":"fall","generatedLink":"/internship/acme.example/fall","publication_type":{"alias":"internship"},"event_status":"preregistration"},
    {"title":"Foreign","company":{"alias":"acme.example","caption":"Acme"},"alias":"foreign","generatedLink":"https://example.org/internship/acme.example/foreign","publication_type":{"alias":"internship"},"event_status":"published"}
  ]],"meta":{"last_page":2,"next_page_url":"http://private-cms/api/publications"}}}}}`
	const detail = `{"query":{"alias_company_or_type":"internship","alias_company_or_page":"acme.example","publication_alias":"summer"},"props":{"pageProps":{"canonicalPath":"/internship/acme.example/summer","data":{"brand":{
  "company":{"alias":"acme.example","caption":"Acme"},"page":{"publication":{"title":"Go Intern","description":"<p>Learn Go</p>","status":"published","event_status":"published","alias":"summer"}}}}}}}`
	for _, tc := range []struct {
		name, source, state, link string
		want, problem             bool
	}{
		{"board", board, catalog, "", true, true},
		{"detail", card, detail, ` <a href="https://acme.example/apply">Подать заявку</a>`, true, false},
		{"unknown level", card, strings.Replace(detail, "Go Intern", "Go Program", 1), "", true, false},
		{"preregistration", card, strings.Replace(detail, `"event_status":"published"`, `"event_status":"preregistration"`, 1), "", true, true},
		{"different card", card, strings.ReplaceAll(detail, "summer", "fall"), "", false, true},
		{"conflicting publication status", board, strings.Replace(catalog, `"event_status":"published"`, `"status":"draft","event_status":"published"`, 1), "", false, true},
		{"broken state", board, `{`, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tc.source {
					t.Fatalf("unexpected request %s", r.URL)
				}
				body := `<script id="__NEXT_DATA__" type="application/json">` + tc.state + `</script>` + tc.link
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r, Header: make(http.Header)}, nil
			})}
			got, err := Fetch(context.Background(), client, tc.source)
			if calls != 1 || (err != nil) != tc.problem || (len(got) == 1) != tc.want || len(got) > 1 {
				t.Fatalf("jobs=%+v requests=%d err=%v", got, calls, err)
			}
			if tc.want {
				job := got[0]
				title, level := "Go Intern", "Intern"
				if tc.name == "unknown level" {
					title, level = "Go Program", ""
				}
				if job.Closed || job.Title != title || job.Company != "Acme" || job.Description != "Learn Go" || job.Type != "Internship" || job.Level != level || job.URL != card || job.SourceURL != tc.source || job.Deadline != "" {
					t.Fatalf("job=%+v", job)
				}
				if tc.link != "" && job.ApplyURL != "https://acme.example/apply" {
					t.Fatalf("application link=%q", job.ApplyURL)
				}
			}
		})
	}
}
