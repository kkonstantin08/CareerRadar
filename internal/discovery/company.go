package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

var errHomepageTruncated = errors.New("homepage exceeds size limit")
var errCareerLinksTruncated = errors.New("career link limit exceeded")

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
	if n := utf8.RuneCountInString(name); n < 2 || n > 100 {
		return nil, errors.New("invalid company name")
	}
	language := "en"
	for _, r := range name {
		if unicode.Is(unicode.Cyrillic, r) {
			language = "ru"
			break
		}
	}
	var search struct {
		Search []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"search"`
	}
	if err := getJSON(ctx, client, "https://www.wikidata.org/w/api.php?action=wbsearchentities&language="+language+"&format=json&limit=3&search="+url.QueryEscape(name), &search); err != nil {
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
		if errors.Is(err, errHomepageTruncated) {
			result.Problem = "Сайт компании прочитан не полностью: превышен лимит размера"
		} else if errors.Is(err, errCareerLinksTruncated) {
			result.Problem = "Найдено больше 20 карьерных ссылок; показана только часть"
		} else if err != nil {
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
	body, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	truncated := len(body) > 1<<20
	if truncated {
		body = body[:1<<20]
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(home.Hostname())
	if err != nil {
		return nil, err
	}
	base := home
	if res.Request != nil && res.Request.URL != nil {
		base = res.Request.URL
	}
	finalRoot, err := publicsuffix.EffectiveTLDPlusOne(base.Hostname())
	if err != nil || finalRoot != root || base.User != nil || (base.Scheme != "https" && base.Scheme != "http") || (home.Scheme == "https" && base.Scheme != "https") {
		return nil, errors.New("homepage redirected to an unverified origin")
	}
	out := []string{}
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(out) > 20 {
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
			ref, e := url.Parse(href)
			if e == nil && href != "" {
				u := base.ResolveReference(ref)
				domain, domainErr := publicsuffix.EffectiveTLDPlusOne(u.Hostname())
				board := supportedCareerBoard(u)
				if board {
					u.Host = u.Hostname()
					u.RawQuery, u.Fragment, u.ForceQuery = "", "", false
				}
				named := strings.Contains(text, "career") || strings.Contains(text, "vacanc") || strings.Contains(text, "jobs") || strings.Contains(text, "ваканс") || strings.Contains(text, "карьер")
				label := strings.ToLower(strings.TrimSpace(nodeText(n)))
				explicit := label == "careers" || label == "career" || label == "jobs" || label == "вакансии" || label == "карьера"
				external := explicit && domainErr == nil && net.ParseIP(u.Hostname()) == nil && u.Scheme == "https" && strings.Split(domain, ".")[0] == strings.Split(root, ".")[0]
				keyURL := *u
				// Tracking parameters do not identify a different career page; fragments may be SPA routes.
				if query, err := url.ParseQuery(keyURL.RawQuery); err == nil {
					for k := range query {
						if strings.HasPrefix(strings.ToLower(k), "utm_") {
							query.Del(k)
						}
					}
					keyURL.RawQuery = query.Encode()
					if keyURL.RawQuery == "" {
						keyURL.ForceQuery = false
					}
				}
				key := keyURL.String()
				if ((domainErr == nil && domain == root && named) || board || external) && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil && !seen[key] {
					seen[key] = true
					out = append(out, u.String())
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	tooMany := len(out) > 20
	if tooMany {
		out = out[:20]
	}
	if truncated {
		return out, errHomepageTruncated
	}
	if tooMany {
		return out, errCareerLinksTruncated
	}
	return out, nil
}

// supportedCareerBoard accepts an explicitly linked board on a supported provider.
func supportedCareerBoard(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.RawPath != "" {
		return false
	}
	switch u.Hostname() {
	case "jobs.lever.co", "jobs.ashbyhq.com", "boards.greenhouse.io", "job-boards.greenhouse.io":
	default:
		return false
	}
	board := strings.Trim(u.Path, "/")
	if board == "" {
		return false
	}
	for _, r := range board {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
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
