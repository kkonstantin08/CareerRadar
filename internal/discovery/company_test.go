package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCompanyDiscoveryProposesCareerLinks(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.URL.Host == "www.wikidata.org" && r.URL.Query().Get("action") == "wbsearchentities":
			body = `{"search":[{"id":"Q1","label":"Example"}]}`
		case r.URL.Host == "www.wikidata.org":
			body = `{"entities":{"Q1":{"claims":{"P856":[{"mainsnak":{"datavalue":{"value":"https://example.org/"}}}]}}}}`
		case r.URL.Host == "example.org":
			body = `<a href="/careers">Careers</a><a href="/about">About</a>`
		default:
			t.Fatalf("unexpected URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	results, err := Company(context.Background(), client, "Example")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].CareerURLs) != 1 || results[0].CareerURLs[0] != "https://example.org/careers" {
		t.Fatalf("results %+v", results)
	}
}

func TestCompanySearchUsesLanguageOfCompanyName(t *testing.T) {
	for _, tc := range []struct{ name, language string }{{"Яндекс", "ru"}, {"Example", "en"}} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if got := r.URL.Query().Get("language"); got != tc.language {
					t.Errorf("language=%q want %q", got, tc.language)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"search":[]}`)), Header: make(http.Header)}, nil
			})}
			if _, err := Company(context.Background(), client, tc.name); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCareerLinksResolveAgainstRedirectedHomepage(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://example.org/about/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<a href="careers">Careers</a>`)), Request: r}, nil
	})}
	home, _ := url.Parse("https://example.org/")
	got, err := careerLinks(context.Background(), client, home)
	if err != nil || len(got) != 1 || got[0] != "https://example.org/about/careers" {
		t.Fatalf("links=%v err=%v", got, err)
	}
}

func TestCareerLinksRejectCrossDomainHomepageRedirect(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "example.org" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.test/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<a href="https://jobs.lever.co/attacker">Jobs</a>`)), Request: r}, nil
	})}
	home, _ := url.Parse("https://example.org/")
	links, err := careerLinks(context.Background(), client, home)
	if err == nil || len(links) != 0 {
		t.Fatalf("links=%v err=%v", links, err)
	}
}

func TestCompanyProposesExplicitExternalCareerLink(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<a href="https://github.careers/">Careers</a><a href="https://evil.example/jobs">About us</a><a href="http://untrusted.example/">Careers</a>`)), Request: r}, nil
	})}
	home, _ := url.Parse("https://github.com/")
	got, err := careerLinks(context.Background(), client, home)
	if err != nil || len(got) != 1 || got[0] != "https://github.careers/" {
		t.Fatalf("links=%v err=%v", got, err)
	}
}

func TestCompanyReportsOversizedHomepageWithoutLosingLinks(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := `<a href="/careers">Careers</a>` + strings.Repeat(" ", 1<<20)
		switch {
		case r.URL.Host == "www.wikidata.org" && r.URL.Query().Get("action") == "wbsearchentities":
			body = `{"search":[{"id":"Q1","label":"Example"}]}`
		case r.URL.Host == "www.wikidata.org":
			body = `{"entities":{"Q1":{"claims":{"P856":[{"mainsnak":{"datavalue":{"value":"https://example.org/"}}}]}}}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	got, err := Company(context.Background(), client, "Example")
	if err != nil || len(got) != 1 || got[0].Problem == "" || len(got[0].CareerURLs) != 1 || got[0].CareerURLs[0] != "https://example.org/careers" {
		t.Fatalf("companies=%+v err=%v", got, err)
	}
}

func TestTruncatedHomepageDoesNotInventPartialCareerLink(t *testing.T) {
	prefix := `<a href="/careers">Careers</a>`
	tail := `<a href="https://example.org/jobs/real-position`
	body := prefix + strings.Repeat(" ", (1<<20)-len(prefix)-len(tail)) + tail + `-suffix">Jobs</a>`
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	home, _ := url.Parse("https://example.org/")
	got, err := careerLinks(context.Background(), client, home)
	if !errors.Is(err, errHomepageTruncated) || len(got) != 1 || got[0] != "https://example.org/careers" {
		t.Fatalf("links=%v err=%v", got, err)
	}
}

func TestTruncatedHomepageDoesNotAcceptUnquotedPartialURL(t *testing.T) {
	prefix := `<a href="/careers">Careers</a>`
	tail := `<a href=https://example.org/jobs/real-position`
	body := prefix + strings.Repeat(" ", (1<<20)-len(prefix)-len(tail)) + tail + `-suffix>Jobs</a>`
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	home, _ := url.Parse("https://example.org/")
	got, err := careerLinks(context.Background(), client, home)
	if !errors.Is(err, errHomepageTruncated) || len(got) != 1 || got[0] != "https://example.org/careers" {
		t.Fatalf("links=%v err=%v", got, err)
	}
}
