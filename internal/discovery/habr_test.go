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
	for _, n := range []int{1, 2, 100, 101} {
		for _, provider := range []string{"jobs", "company"} {
			calls := 0
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				body := `{"search":[]}`
				if r.URL.Host == "career.habr.com" {
					body = `<rss><channel></channel></rss>`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			var err error
			if provider == "jobs" {
				_, err = Search(context.Background(), client, strings.Repeat("я", n))
			} else {
				_, err = Company(context.Background(), client, strings.Repeat("я", n))
			}
			valid := n >= 2 && n <= 100
			if (err == nil) != valid || (calls > 0) != valid {
				t.Errorf("%s length %d calls=%d err=%v", provider, n, calls, err)
			}
		}
	}
}

func TestHabrSearchRetainsOnlyRelevantAttributedVacancies(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "career.habr.com" || r.URL.Path != "/vacancies/rss" || r.URL.Query().Get("q") != "Go" {
			t.Errorf("unexpected search endpoint %s", r.URL)
		}
		body := `<rss version="2.0"><channel>
  <item><title>Junior Go Developer</title><author>Example Engineering</author><link>https://career.habr.com/vacancies/1000000001</link><description>Казань (Россия). Можно удалённо. Go и SQL.</description></item>
  <item><title>Writer</title><link>https://career.habr.com/vacancies/1000000002</link><description>Write articles</description></item>
  <item><title>Go Engineer</title><link>https://evil.test/vacancies/1000000003</link><description>Go</description></item>
  <item><title>Backend Engineer</title><author>Example</author><link>https://career.habr.com/vacancies/1000000004</link><description>Use Go daily</description></item>
  <item><title>Writer</title><link>https://career.habr.com/vacancies/1000000005</link><description>&lt;script&gt;Go&lt;/script&gt;Write articles</description></item>
  </channel></rss>`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	got, err := Search(context.Background(), client, "Go")
	if err != nil || len(got) != 2 {
		t.Fatalf("results=%+v err=%v", got, err)
	}
	if got[0].Company != "Example Engineering" || got[0].Source != "Хабр Карьера" || got[0].WorkMode != "remote" || got[0].Location != "" {
		t.Fatalf("published facts=%+v", got[0])
	}
	if got[1].WorkMode != "" {
		t.Fatalf("invented remote mode: %+v", got[1])
	}
}

func TestHabrSearchHandlesEmptyInvalidAndUnavailableFeeds(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		valid  bool
	}{
		{`<rss><channel></channel></rss>`, 200, true},
		{`<rss><channel>`, 200, false},
		{`<html>Access restricted</html>`, 200, false},
		{`Forbidden`, 403, false},
	} {
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		got, err := Search(context.Background(), client, "Go")
		if (err == nil) != tc.valid || len(got) != 0 {
			t.Errorf("body=%s results=%+v err=%v", tc.body, got, err)
		}
	}
}

func TestDescriptionOmitsScriptsAndStyles(t *testing.T) {
	got := plainDescription(`<p>Requirements: Go</p><script>untrusted()</script><style>.hidden{}</style><p>Remote</p>`)
	if !strings.Contains(got, "Requirements: Go") || !strings.Contains(got, "Remote") || strings.Contains(got, "untrusted") || strings.Contains(got, "hidden") {
		t.Fatalf("description %q", got)
	}
}

func TestVacancyURLRejectsUnsafeOrNonIndividualLinks(t *testing.T) {
	for _, raw := range []string{
		"https://career.habr.com/vacancies", "https://career.habr.com/vacancies/0", "https://career.habr.com/vacancies/0123",
		"https://career.habr.com.evil.test/vacancies/123", "https://user@career.habr.com/vacancies/123", "http://career.habr.com/vacancies/123",
		"https://career.habr.com:444/vacancies/123", "https://career.habr.com/vacancies/123?other=1", "https://career.habr.com/vacancies/123#other",
	} {
		if got := VacancyURL(raw); got != "" {
			t.Errorf("unsafe vacancy %q became %q", raw, got)
		}
	}
	if got := VacancyURL("https://career.habr.com:443/vacancies/123/"); got != "https://career.habr.com/vacancies/123" {
		t.Fatalf("canonical=%q", got)
	}
}
