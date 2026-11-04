package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

// CompanyResult is a candidate company and unconfirmed career pages.
type CompanyResult struct {
	Name       string   `json:"name"`
	Homepage   string   `json:"homepage"`
	CareerURLs []string `json:"career_urls"`
	Problem    string   `json:"problem,omitempty"`
}

// Company finds candidate company websites via Wikidata and proposes career links.
func Company(ctx context.Context, client *http.Client, name string) ([]CompanyResult, error) {
	name = strings.TrimSpace(name)
	if len(name) < 2 || len(name) > 100 {
		return nil, errors.New("invalid company name")
	}
	var search struct {
		Search []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"search"`
	}
	if err := getJSON(ctx, client, "https://www.wikidata.org/w/api.php?action=wbsearchentities&language=en&format=json&limit=3&search="+url.QueryEscape(name), &search); err != nil {
		return nil, err
	}
	out := []CompanyResult{}
	for _, candidate := range search.Search {
		id := candidate.ID
		if len(id) < 2 || id[0] != 'Q' {
			continue
		}
		valid := true
		for _, c := range id[1:] {
			if c < '0' || c > '9' {
				valid = false
			}
		}
		if !valid {
			continue
		}
		var detail struct {
			Entities map[string]struct {
				Claims struct {
					Website []struct {
						Main struct {
							Data struct {
								Value string `json:"value"`
							} `json:"datavalue"`
						} `json:"mainsnak"`
					} `json:"P856"`
				} `json:"claims"`
			} `json:"entities"`
		}
		if err := getJSON(ctx, client, "https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&props=claims&ids="+id, &detail); err != nil {
			continue
		}
		claims := detail.Entities[id].Claims.Website
		if len(claims) == 0 {
			continue
		}
		home, err := url.Parse(claims[0].Main.Data.Value)
		if err != nil || home.Scheme != "https" || home.Hostname() == "" || home.User != nil {
			continue
		}
		result := CompanyResult{Name: candidate.Label, Homepage: home.String(), CareerURLs: []string{}}
		result.CareerURLs, err = careerLinks(ctx, client, home)
		if err != nil {
			result.Problem = "Не удалось прочитать сайт компании"
		}
		out = append(out, result)
	}
	return out, nil
}

func getJSON(ctx context.Context, client *http.Client, uri string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "CareerRadar/1.0 (personal job search)")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("discovery source unavailable")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(target)
}

func careerLinks(ctx context.Context, client *http.Client, home *url.URL) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, home.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CareerRadar/1.0 (personal job search)")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("homepage unavailable")
	}
	doc, err := html.Parse(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(home.Hostname())
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(out) >= 20 {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			var href string
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
			text := strings.ToLower(href + " " + nodeText(n))
			if strings.Contains(text, "career") || strings.Contains(text, "vacanc") || strings.Contains(text, "jobs") || strings.Contains(text, "ваканс") || strings.Contains(text, "карьер") {
				ref, e := url.Parse(href)
				if e == nil {
					u := home.ResolveReference(ref)
					domain, e := publicsuffix.EffectiveTLDPlusOne(u.Hostname())
					if e == nil && domain == root && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil && !seen[u.String()] {
						seen[u.String()] = true
						out = append(out, u.String())
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
