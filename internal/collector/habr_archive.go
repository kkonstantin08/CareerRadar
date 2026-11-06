package collector

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// HabrArchiveEvidenceURL validates an individual vacancy URL used as archive evidence.
func HabrArchiveEvidenceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !habrVacancyURL(u) || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return ""
	}
	return raw
}

func checkHabrArchive(ctx context.Context, client *http.Client, raw string) (bool, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("User-Agent", "CareerRadar/1.0 (+personal opportunity monitor)")
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noRedirect.Do(req)
	if err != nil {
		return false, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusGone {
		return false, nil, errors.New("Habr vacancy unavailable; source requires attention")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		return false, nil, err
	}
	if len(body) > 2<<20 {
		return false, nil, errResponseTooLarge
	}
	archived := habrArchived(body)
	if res.StatusCode == http.StatusGone && !archived {
		return false, nil, errors.New("Habr vacancy unavailable without archive confirmation; source requires attention")
	}
	return archived, body, nil
}

// habrArchived recognizes the archive DOM marker outside explicitly hidden content; external CSS is not evaluated.
func habrArchived(body []byte) bool {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return false
	}
	var visit func(*html.Node) bool
	visit = func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			if n.Data == "script" || n.Data == "style" || n.Data == "template" {
				return false
			}
			for _, a := range n.Attr {
				if a.Key == "hidden" || a.Key == "aria-hidden" && strings.EqualFold(strings.TrimSpace(a.Val), "true") {
					return false
				}
				if a.Key == "style" {
					style := strings.ToLower(strings.Join(strings.Fields(a.Val), ""))
					if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") {
						return false
					}
				}
			}
			for _, a := range n.Attr {
				if a.Key == "class" {
					for _, class := range strings.Fields(a.Val) {
						if class == "no-content__title" && strings.Join(strings.Fields(textContent(n)), " ") == "Вакансия в архиве" {
							return true
						}
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(doc)
}
