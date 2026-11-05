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
	return fetch(ctx, client, rawURL, "")
}

// FetchJobicyStatus fetches a Jobicy page using a previously verified batch status.
func FetchJobicyStatus(ctx context.Context, client *http.Client, rawURL, status string) ([]Job, error) {
	if status != "active" && status != "closed" && status != "unknown" {
		return nil, errors.New("invalid Jobicy status")
	}
	return fetch(ctx, client, rawURL, status)
}

func fetch(ctx context.Context, client *http.Client, rawURL, status string) ([]Job, error) {
	root, err := url.Parse(rawURL)
	if err != nil || (root.Scheme != "https" && root.Scheme != "http") || root.Hostname() == "" || root.User != nil {
		return nil, errors.New("invalid source URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id := jobicyID(root)
	if status != "" && id == "" {
		return nil, errors.New("invalid Jobicy job URL")
	}
	if id != "" {
		statusURL := "https://jobicy.com/api/jobs/status?ids=" + id
		if status == "" {
			statuses, err := JobicyStatuses(ctx, client, []string{id})
			if err != nil {
				return nil, err
			}
			status = statuses[id]
		}
		switch status {
		case "closed":
			return []Job{{URL: root.String(), SourceURL: statusURL, Closed: true}}, nil
		case "active":
		default:
			return nil, errors.New("Jobicy status unknown; source requires attention")
		}
	}
	if api := greenhouseURL(root); api != "" {
		body, _, err := readPage(ctx, client, api)
		var responseErr *httpStatusError
		// A 404 is closure evidence only when the exact job endpoint returned it without redirecting.
		if errors.As(err, &responseErr) && errors.Is(err, errNotFound) && responseErr.url == api {
			if evidence := GreenhouseClosureEvidenceURL(root.String()); evidence != "" {
				return []Job{{URL: root.String(), SourceURL: evidence, Closed: true}}, nil
			}
		}
		if errors.Is(err, errResponseTooLarge) && strings.HasSuffix(api, "?content=true") {
			body, _, err = readPage(ctx, client, strings.TrimSuffix(api, "?content=true"))
			if err == nil {
				jobs, parseErr := parseGreenhouse(body, root.String())
				if parseErr != nil {
					return nil, parseErr
				}
				return jobs, errors.New("Greenhouse descriptions exceeded response limit; source requires attention")
			}
		}
		if err != nil {
			return nil, err
		}
		return parseGreenhouse(body, root.String())
	}
	if api := ashbyURL(root); api != "" {
		body, actual, err := readPageLimit(ctx, client, api, 8<<20)
		if err != nil {
			return nil, err
		}
		if actual != api {
			return nil, errors.New("unexpected Ashby API redirect; source requires attention")
		}
		return parseAshby(body, root.String())
	}
	if site, id, ok := leverBoard(root); ok {
		api := "https://api.lever.co/v0/postings/" + url.PathEscape(site) + "?mode=xml"
		body, actual, err := readPage(ctx, client, api)
		if err != nil {
			return nil, err
		}
		if actual != api {
			return nil, errors.New("unexpected Lever API redirect; source requires attention")
		}
		return parseLever(body, root.String(), site, id)
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
		if len(extracted) == 0 {
			partial, ok, fallbackErr := parseHTMLJob(body, actual)
			if ok {
				extracted = []Job{partial}
				if fallbackErr != nil {
					problem = fallbackErr
				} else {
					problem = errors.New("incomplete job page; source requires attention")
				}
			}
		}
		pageURL, err := url.Parse(actual)
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
			} else {
				ref, err := url.Parse(j.URL)
				if err != nil {
					problem = errors.New("invalid vacancy URL; source requires attention")
					continue
				}
				resolved := pageURL.ResolveReference(ref)
				if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Hostname() == "" || resolved.User != nil {
					problem = errors.New("invalid vacancy URL; source requires attention")
					continue
				}
				j.URL = resolved.String()
			}
			if len(extracted) == 1 && j.URL == actual {
				apply, err := applicationLink(body, pageURL)
				if err != nil {
					problem = err
				} else {
					j.ApplyURL = apply
				}
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

func applicationLink(body []byte, base *url.URL) (string, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	links := map[string]bool{}
	invalid := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			label := strings.ToLower(strings.Join(strings.Fields(textContent(n)), " "))
			if label == "apply" || label == "apply now" || label == "откликнуться" || label == "подать заявку" {
				for _, attr := range n.Attr {
					if attr.Key != "href" || strings.TrimSpace(attr.Val) == "" {
						continue
					}
					ref, err := url.Parse(attr.Val)
					if err != nil {
						invalid = true
						continue
					}
					u := base.ResolveReference(ref)
					if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
						invalid = true
						continue
					}
					links[u.String()] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if invalid || len(links) > 1 {
		return "", errors.New("application link is ambiguous or invalid; source requires attention")
	}
	for link := range links {
		return link, nil
	}
	return "", nil
}

// shortcut: fallback only recognizes individual job URLs with an explicit Apply link; unsupported layouts need a source-specific adapter.
func parseHTMLJob(body []byte, source string) (Job, bool, error) {
	u, err := url.Parse(source)
	if err != nil || !individualJobURL(u) {
		return Job{}, false, nil
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return Job{}, false, err
	}
	title := ""
	ambiguous := false
	var findTitle func(*html.Node)
	findTitle = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "h1" {
			value := strings.Join(strings.Fields(textContent(n)), " ")
			if title != "" && value != title {
				ambiguous = true
			} else if value != "" {
				title = value
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			findTitle(child)
		}
	}
	findTitle(doc)
	if title == "" || ambiguous {
		return Job{}, false, nil
	}
	applyURL, err := applicationLink(body, u)
	job := Job{URL: source, SourceURL: source, Title: title}
	if err != nil {
		return job, true, err
	}
	if applyURL == "" {
		return Job{}, false, nil
	}
	job.ApplyURL = applyURL
	job.Level, _ = titleLevel(title)
	job.Type = titleType(title)
	return job, true, nil
}

func individualJobURL(u *url.URL) bool {
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts[:len(parts)-1] {
		switch strings.ToLower(part) {
		case "job", "jobs", "vacancy", "vacancies", "position", "positions":
			return i+1 < len(parts) && parts[i+1] != ""
		}
	}
	return false
}

// JobicyID returns the listing ID for a supported individual Jobicy URL.
func JobicyID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return jobicyID(u)
}

// JobicyStatuses reads one bounded batch of public listing statuses.
func JobicyStatuses(ctx context.Context, client *http.Client, ids []string) (map[string]string, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("invalid Jobicy batch size")
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		n, err := strconv.ParseUint(id, 10, 53)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != id || wanted[id] {
			return nil, errors.New("invalid Jobicy ID")
		}
		wanted[id] = true
	}
	uri := "https://jobicy.com/api/jobs/status?ids=" + strings.Join(ids, ",")
	body, actual, err := readPage(ctx, client, uri)
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
	if actual != uri || json.Unmarshal(body, &response) != nil || !response.Success || len(response.Jobs) != len(ids) {
		return nil, errors.New("invalid Jobicy status response; source requires attention")
	}
	result := make(map[string]string, len(ids))
	for _, job := range response.Jobs {
		id := job.ID.String()
		if !wanted[id] || result[id] != "" || (job.Status != "active" && job.Status != "closed" && job.Status != "unknown") {
			return nil, errors.New("invalid Jobicy status response; source requires attention")
		}
		result[id] = job.Status
	}
	return result, nil
}

func jobicyID(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if !strings.EqualFold(u.Scheme, "https") || (host != "jobicy.com" && host != "www.jobicy.com") || (u.Port() != "" && u.Port() != "443") || u.User != nil || u.RawPath != "" {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	for key := range query {
		if !strings.HasPrefix(strings.ToLower(key), "utm_") {
			return ""
		}
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimSuffix(u.Path, "/"), "/"), "/")
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

// JobicyCanonicalURL normalizes supported aliases without discarding job-identifying parameters.
func JobicyCanonicalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || jobicyID(u) == "" {
		return ""
	}
	u.Scheme, u.Host, u.RawQuery, u.Fragment = "https", "jobicy.com", "", ""
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}

// GreenhouseClosureEvidenceURL returns the exact public endpoint for one known Greenhouse job.
func GreenhouseClosureEvidenceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || (u.Hostname() != "boards.greenhouse.io" && u.Hostname() != "job-boards.greenhouse.io") || (u.Port() != "" && u.Port() != "443") || u.User != nil || u.RawPath != "" || u.ForceQuery {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[1] != "jobs" || parts[0] == "" {
		return ""
	}
	for _, r := range parts[0] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	id, err := strconv.ParseUint(parts[2], 10, 53)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != parts[2] {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	hasJobID := false
	for key, values := range query {
		if strings.EqualFold(key, "gh_jid") {
			if hasJobID || len(values) != 1 || values[0] != parts[2] {
				return ""
			}
			hasJobID = true
		} else if !strings.HasPrefix(strings.ToLower(key), "utm_") {
			return ""
		}
	}
	return "https://boards-api.greenhouse.io/v1/boards/" + parts[0] + "/jobs/" + parts[2]
}

// GreenhouseCanonicalURL removes tracking data from a supported individual job link.
func GreenhouseCanonicalURL(raw string) string {
	if GreenhouseClosureEvidenceURL(raw) == "" {
		return ""
	}
	u, _ := url.Parse(raw)
	u.Fragment = ""
	query := u.Query()
	for key := range query {
		if strings.HasPrefix(strings.ToLower(key), "utm_") {
			query.Del(key)
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
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

var errResponseTooLarge = errors.New("source response too large")
var errNotFound = errors.New("source returned HTTP 404")

type httpStatusError struct {
	status int
	url    string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("source returned HTTP %s", strconv.Itoa(e.status))
}

func (e *httpStatusError) Unwrap() error {
	if e.status == http.StatusNotFound {
		return errNotFound
	}
	return nil
}

func readPage(ctx context.Context, client *http.Client, uri string) ([]byte, string, error) {
	return readPageLimit(ctx, client, uri, 2<<20)
}

func readPageLimit(ctx context.Context, client *http.Client, uri string, limit int64) ([]byte, string, error) {
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
		actual := req.URL.String()
		if res.Request != nil && res.Request.URL != nil {
			actual = res.Request.URL.String()
		}
		return nil, "", &httpStatusError{status: res.StatusCode, url: actual}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > limit {
		return nil, "", errResponseTooLarge
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
	api := "https://boards-api.greenhouse.io/v1/boards/" + strings.Join(parts, "/")
	if len(parts) == 1 {
		api += "/jobs?content=true"
	}
	return api
}

func parseGreenhouse(body []byte, source string) ([]Job, error) {
	type item struct {
		Title    string `json:"title"`
		Company  string `json:"company_name"`
		URL      string `json:"absolute_url"`
		Deadline string `json:"application_deadline"`
		Content  string `json:"content"`
		Location struct {
			Name string `json:"name"`
		} `json:"location"`
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
		level, _ := titleLevel(x.Title)
		offerType := titleType(x.Title)
		jobs = append(jobs, Job{Title: x.Title, Company: x.Company, URL: x.URL, Deadline: x.Deadline, Description: x.Content, Location: x.Location.Name, Type: offerType, Level: level, SourceURL: source})
	}
	return jobs, nil
}
