package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client returns a bounded client that refuses connections to private networks.
func Client() *http.Client {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 8, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("no IP addresses")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("private address blocked")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("unsafe redirect")
		}
		if IsJobicyURL(req.URL.String()) && !IsJobicyURL(via[0].URL.String()) {
			return errors.New("redirect to Jobicy requires a direct source URL")
		}
		return nil
	}}
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "2001:db8::/32"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}

// Fetch follows relevant same-host links with strict page, depth, and time limits.
// It returns partial observations alongside an error if any page was unavailable.
func Fetch(ctx context.Context, client *http.Client, rawURL string) ([]Job, error) {
	root, err := url.Parse(rawURL)
	if err != nil || (root.Scheme != "https" && root.Scheme != "http") || root.Hostname() == "" || root.User != nil {
		return nil, errors.New("invalid source URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id := jobicyID(root)
	if id != "" {
		statusURL := "https://jobicy.com/api/jobs/status?ids=" + id
		body, actual, err := readPage(ctx, client, statusURL)
		if err != nil {
			return nil, err
		}
		var response struct {
			Success bool `json:"success"`
			Jobs    []struct {
				ID     json.Number `json:"id"`
				Status string      `json:"status"`
			} `json:"jobs"`
		}
		if actual != statusURL || json.Unmarshal(body, &response) != nil || !response.Success || len(response.Jobs) != 1 || response.Jobs[0].ID.String() != id {
			return nil, errors.New("invalid Jobicy status response; source requires attention")
		}
		switch response.Jobs[0].Status {
		case "closed":
			return []Job{{URL: root.String(), SourceURL: statusURL, Closed: true}}, nil
		case "active":
		default:
			return nil, errors.New("Jobicy status unknown; source requires attention")
		}
	}
	if api := greenhouseURL(root); api != "" {
		body, _, err := readPage(ctx, client, api)
		if err != nil {
			return nil, err
		}
		return parseGreenhouse(body, root.String())
	}
	type page struct {
		url   string
		depth int
	}
	queue := []page{{root.String(), 0}}
	seen := map[string]bool{root.String(): true}
	var found []Job
	var problem error
	for len(queue) > 0 && len(seen) <= 20 {
		current := queue[0]
		queue = queue[1:]
		body, actual, err := readPage(ctx, client, current.url)
		if err != nil {
			problem = err
			continue
		}
		extracted, err := Parse(bytes.NewReader(body), actual)
		if err != nil {
			problem = err
			continue
		}
		for _, j := range extracted {
			if j.URL == "" {
				if len(extracted) > 1 {
					problem = errors.New("multiple vacancies have no distinct URL; source requires attention")
					continue
				}
				j.URL = actual
			}
			found = append(found, j)
		}
		if current.depth >= 2 || id != "" {
			continue
		}
		base, err := url.Parse(actual)
		if err != nil {
			continue
		}
		for _, link := range relevantLinks(body, base) {
			if !seen[link] && len(seen) < 20 {
				seen[link] = true
				queue = append(queue, page{link, current.depth + 1})
			} else if !seen[link] {
				problem = errors.New("page limit reached; source requires attention")
			}
		}
	}
	if len(found) == 0 && problem == nil {
		problem = errors.New("no structured vacancies found; source requires attention")
	}
	return found, problem
}

func jobicyID(u *url.URL) string {
	if u.Scheme != "https" || u.Host != "jobicy.com" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "jobs" {
		return ""
	}
	id, _, _ := strings.Cut(parts[1], "-")
	n, err := strconv.ParseUint(id, 10, 53)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != id {
		return ""
	}
	return id
}

// IsJobicyURL reports whether a URL belongs to Jobicy.
func IsJobicyURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == "jobicy.com" || strings.HasSuffix(host, ".jobicy.com")
}

func readPage(ctx context.Context, client *http.Client, uri string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "CareerRadar/1.0 (+personal opportunity monitor)")
	res, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("source returned HTTP %s", strconv.Itoa(res.StatusCode))
	}
	const limit = 2 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > limit {
		return nil, "", errors.New("source response too large")
	}
	actual := uri
	if res.Request != nil && res.Request.URL != nil {
		actual = res.Request.URL.String()
	}
	return body, actual, nil
}

func relevantLinks(body []byte, base *url.URL) []string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	out := []string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			var href, rel string
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
				if a.Key == "rel" {
					rel = a.Val
				}
			}
			label := strings.ToLower(href + " " + rel + " " + textContent(n))
			if href != "" && (strings.Contains(label, "job") || strings.Contains(label, "career") || strings.Contains(label, "vacan") || strings.Contains(label, "ваканс") || strings.Contains(label, "стаж") || strings.Contains(label, "next")) {
				ref, e := url.Parse(href)
				if e == nil {
					u := base.ResolveReference(ref)
					u.Fragment = ""
					if u.Host == base.Host && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") {
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
	return out
}

func textContent(n *html.Node) string {
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

// greenhouseURL maps supported public job-board URLs to their documented API.
func greenhouseURL(u *url.URL) string {
	if u.Hostname() != "boards.greenhouse.io" && u.Hostname() != "job-boards.greenhouse.io" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 1 && !(len(parts) == 3 && parts[1] == "jobs") {
		return ""
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return ""
			}
		}
	}
	return "https://boards-api.greenhouse.io/v1/boards/" + strings.Join(parts, "/")
}

func parseGreenhouse(body []byte, source string) ([]Job, error) {
	type item struct {
		Title    string `json:"title"`
		Company  string `json:"company_name"`
		URL      string `json:"absolute_url"`
		Deadline string `json:"application_deadline"`
		Content  string `json:"content"`
	}
	var list struct {
		Jobs []item `json:"jobs"`
	}
	var items []item
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("invalid Greenhouse response: %w", err)
	}
	items = list.Jobs
	if items == nil {
		var x item
		if err := json.Unmarshal(body, &x); err != nil {
			return nil, err
		}
		if x.Title != "" {
			items = []item{x}
		}
	}
	if len(items) == 0 {
		return nil, errors.New("no vacancies returned; source requires attention")
	}
	if len(items) > 1000 {
		return nil, errors.New("too many vacancies; source requires attention")
	}
	jobs := make([]Job, 0, len(items))
	for _, x := range items {
		jobs = append(jobs, Job{Title: x.Title, Company: x.Company, URL: x.URL, Deadline: x.Deadline, Description: x.Content, SourceURL: source})
	}
	return jobs, nil
}
