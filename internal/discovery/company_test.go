package discovery

import (
	"context"
	"io"
	"net/http"
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
