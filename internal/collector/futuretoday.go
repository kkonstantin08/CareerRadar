package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

const futureTodayOrigin = "https://it.fut.ru"

var futureTodaySlug = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

func futureTodayPath(u *url.URL) string {
	if u.Scheme != "https" || u.Hostname() != "it.fut.ru" || u.User != nil || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	for key := range query {
		if !strings.HasPrefix(key, "utm_") {
			return ""
		}
	}
	path := strings.TrimSuffix(u.Path, "/")
	parts := strings.Split(path, "/")
	if path == "/internship" || len(parts) == 4 && parts[1] == "internship" && futureTodaySlug.MatchString(parts[2]) && futureTodaySlug.MatchString(parts[3]) {
		return path
	}
	return ""
}

type futureTodayCompany struct{ Alias, Caption string }

type futureTodayPublication struct {
	Title, Description, Alias, GeneratedLink, Status string
	EventStatus                                      string                 `json:"event_status"`
	PublicationType                                  struct{ Alias string } `json:"publication_type"`
	Company                                          futureTodayCompany
}

func (p futureTodayPublication) job(path, evidence, company string) Job {
	offer, level := TitleFacts(p.Title)
	if offer == "Job" {
		offer = "Internship"
	}
	return Job{Title: strings.TrimSpace(p.Title), Company: strings.TrimSpace(company), Description: structuredText(p.Description), Type: offer, Level: level, URL: futureTodayOrigin + path, SourceURL: evidence}
}

func fetchFutureToday(ctx context.Context, client *http.Client, path string) ([]Job, error) {
	source := futureTodayOrigin + path
	// A redirect can replace a specific program with a different card or a catalog.
	direct := *client
	direct.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	body, actual, err := readPage(ctx, &direct, source)
	if err != nil {
		return nil, err
	}
	if actual != source {
		return nil, errors.New("unexpected FutureToday response URL; source requires attention")
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var payload []byte
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode || node.Data != "script" || attribute(node, "id") != "__NEXT_DATA__" || attribute(node, "type") != "application/json" {
			continue
		}
		if payload != nil {
			return nil, errors.New("ambiguous FutureToday page data; source requires attention")
		}
		payload = []byte(textContent(node))
	}
	var page struct {
		Query struct {
			Type    string `json:"alias_company_or_type"`
			Company string `json:"alias_company_or_page"`
			Alias   string `json:"publication_alias"`
		}
		Props struct {
			PageProps struct {
				CanonicalPath string
				Data          struct {
					Publications [][]futureTodayPublication
					Meta         struct {
						LastPage int    `json:"last_page"`
						NextPage string `json:"next_page_url"`
					}
					Brand struct {
						Company futureTodayCompany
						Page    struct{ Publication futureTodayPublication }
					}
				}
			}
		}
	}
	if json.Unmarshal(payload, &page) != nil || page.Query.Type != "internship" {
		return nil, errors.New("invalid FutureToday page data; source requires attention")
	}
	data := page.Props.PageProps.Data
	if path == "/internship" {
		return futureTodayCatalog(source, data.Publications, data.Meta.LastPage > 1 || data.Meta.NextPage != "")
	}
	card := data.Brand.Page.Publication
	company := data.Brand.Company
	if page.Props.PageProps.CanonicalPath != path || company.Alias != page.Query.Company || card.Alias != page.Query.Alias || "/internship/"+company.Alias+"/"+card.Alias != path || strings.TrimSpace(card.Title) == "" || card.Status != "published" {
		return nil, errors.New("could not verify FutureToday program card; source requires attention")
	}
	job := card.job(path, source, company.Caption)
	if card.EventStatus != "published" {
		return []Job{job}, errors.New("FutureToday application availability requires clarification")
	}
	base, _ := url.Parse(source)
	job.ApplyURL, err = applicationLink(body, base)
	return []Job{job}, err
}

func futureTodayCatalog(source string, groups [][]futureTodayPublication, more bool) ([]Job, error) {
	base, _ := url.Parse(source)
	seen := make(map[string]bool)
	var found []Job
	var problem error
	for _, group := range groups {
		for _, card := range group {
			if card.EventStatus != "published" || card.PublicationType.Alias != "internship" {
				continue
			}
			// Catalog cards can omit the publication status, unlike individual pages.
			if card.Status != "" && card.Status != "published" {
				problem = errors.New("conflicting FutureToday publication status; source requires attention")
				continue
			}
			ref, err := url.Parse(card.GeneratedLink)
			if err != nil || card.GeneratedLink == "" {
				problem = errors.New("invalid FutureToday catalog card; source requires attention")
				continue
			}
			path := futureTodayPath(base.ResolveReference(ref))
			if path == "" || path != "/internship/"+card.Company.Alias+"/"+card.Alias || strings.TrimSpace(card.Title) == "" {
				problem = errors.New("invalid FutureToday catalog card; source requires attention")
				continue
			}
			if seen[path] {
				continue
			}
			if len(found) == 20 {
				return found, errors.New("FutureToday catalog limit reached; source requires attention")
			}
			seen[path] = true
			found = append(found, card.job(path, source, card.Company.Caption))
		}
	}
	// Continuation URLs in the embedded state can point to the provider's internal CMS.
	if more {
		problem = errors.Join(problem, errors.New("FutureToday catalog publishes additional pages without a verified public continuation; source requires attention"))
	}
	if len(found) == 0 && problem == nil {
		problem = errors.New("no verified FutureToday internships found; source requires attention")
	}
	return found, problem
}
