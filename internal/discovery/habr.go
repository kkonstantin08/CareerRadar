// Package discovery searches public job sources independently of saved sites.
package discovery

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// SourceName attributes the current public vacancy discovery feed.
const SourceName = "Хабр Карьера"

// SourceURL identifies the discovery source without enabling its monitoring.
const SourceURL = "https://career.habr.com/vacancies"

// Result is an attributed vacancy with only explicitly published facts.
type Result struct {
	Title       string `json:"title"`
	Company     string `json:"company"`
	URL         string `json:"url"`
	Location    string `json:"location"`
	WorkMode    string `json:"work_mode"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// VacancyURL validates and canonicalizes an individual Habr Career vacancy link.
func VacancyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "career.habr.com" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "vacancies" {
		return ""
	}
	id, err := strconv.ParseUint(parts[1], 10, 53)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != parts[1] {
		return ""
	}
	return SourceURL + "/" + parts[1]
}

// Search reads a bounded public Habr Career RSS search, without authentication or a browser.
func Search(ctx context.Context, client *http.Client, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if n := utf8.RuneCountInString(query); n < 2 || n > 100 {
		return nil, errors.New("search query must contain 2 to 100 characters")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL+"/rss?q="+url.QueryEscape(query)+"&type=all", nil)
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
		return nil, errors.New("Habr Career search unavailable")
	}
	var feed struct {
		XMLName xml.Name `xml:"rss"`
		Channel *struct {
			Items []struct {
				Title       string `xml:"title"`
				Company     string `xml:"author"`
				URL         string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err = xml.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&feed); err != nil {
		return nil, err
	}
	if feed.Channel == nil {
		return nil, errors.New("invalid Habr Career RSS feed")
	}
	results := []Result{}
	needle := strings.ToLower(strings.Join(strings.Fields(query), " "))
	matches := func(value string) bool {
		return strings.Contains(strings.ToLower(strings.Join(strings.Fields(value), " ")), needle)
	}
	for _, item := range feed.Channel.Items {
		link := VacancyURL(item.URL)
		if link == "" || strings.TrimSpace(item.Title) == "" {
			continue
		}
		description := plainDescription(item.Description)
		if !matches(item.Title) && !matches(description) {
			continue
		}
		mode := ""
		// The feed publishes this marker explicitly; omitted office, hours and eligibility stay unknown.
		if strings.Contains(description, "Можно удалённо.") {
			mode = "remote"
		}
		results = append(results, Result{Title: item.Title, Company: strings.TrimSpace(item.Company), URL: link, Source: SourceName, Description: description, WorkMode: mode})
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
