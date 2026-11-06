// Package discovery searches public job sources independently of saved sites.
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Result is a job search result attributed to its public source.
type Result struct {
	Title       string `json:"title"`
	Company     string `json:"company"`
	URL         string `json:"url"`
	Location    string `json:"location"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// Search queries Remotive's public API and returns attributed links.
func Search(ctx context.Context, client *http.Client, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if n := utf8.RuneCountInString(query); n < 2 || n > 100 {
		return nil, errors.New("search query must contain 2 to 100 characters")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://remotive.com/api/remote-jobs?search="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CareerRadar/1.0 (personal job search)")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("Remotive search unavailable")
	}
	var data struct {
		Jobs []struct {
			Title       string `json:"title"`
			Company     string `json:"company_name"`
			URL         string `json:"url"`
			Location    string `json:"candidate_required_location"`
			Description string `json:"description"`
		} `json:"jobs"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&data); err != nil {
		return nil, err
	}
	results := []Result{}
	for _, j := range data.Jobs {
		link, err := url.Parse(j.URL)
		if err != nil || link.Scheme != "https" || link.Hostname() != "remotive.com" || link.User != nil || j.Title == "" {
			continue
		}
		results = append(results, Result{Title: j.Title, Company: j.Company, URL: j.URL, Location: j.Location, Source: "Remotive", Description: plainDescription(j.Description)})
		if len(results) == 50 {
			break
		}
	}
	return results, nil
}

func plainDescription(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	var parts []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			if text := strings.TrimSpace(n.Data); text != "" {
				parts = append(parts, text)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return strings.Join(parts, "\n")
}
