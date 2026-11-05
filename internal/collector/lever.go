package collector

import (
	"encoding/xml"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var leverSite = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var leverPostingID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func leverBoard(u *url.URL) (string, string, bool) {
	if u.Scheme != "https" || u.Hostname() != "jobs.lever.co" || (u.Port() != "" && u.Port() != "443") || u.User != nil || u.RawPath != "" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 1 || len(parts) > 3 || !leverSite.MatchString(parts[0]) {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if !leverPostingID.MatchString(parts[1]) {
		return "", "", false
	}
	if len(parts) == 3 && parts[2] != "apply" {
		return "", "", false
	}
	return parts[0], strings.ToLower(parts[1]), true
}

func leverApplyURL(site, id, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "jobs.lever.co" || (u.Port() != "" && u.Port() != "443") || u.User != nil || u.RawPath != "" || u.Fragment != "" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], site) || !strings.EqualFold(parts[1], id) || parts[2] != "apply" {
		return ""
	}
	return u.String()
}

func parseLever(body []byte, source, site, targetID string) ([]Job, error) {
	var feed struct {
		Jobs []struct {
			ID          string `xml:"id"`
			Position    string `xml:"position"`
			Description string `xml:"description"`
			ApplyURL    string `xml:"apply_url"`
			Employer    string `xml:"employer"`
			Location    string `xml:"location"`
			Category    string `xml:"category"`
			Commitment  string `xml:"commitment"`
		} `xml:"job"`
	}
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, errors.New("invalid Lever postings feed")
	}
	var jobs []Job
	problem := false
	for _, posting := range feed.Jobs {
		id := strings.ToLower(strings.TrimSpace(posting.ID))
		if targetID != "" && id != targetID {
			continue
		}
		if !leverPostingID.MatchString(id) {
			problem = true
			continue
		}
		applyURL := leverApplyURL(site, id, strings.TrimSpace(posting.ApplyURL))
		if applyURL == "" {
			problem = true
		}
		level, ambiguous := titleLevel(posting.Position)
		offerType := titleType(posting.Position)
		if strings.EqualFold(strings.TrimSpace(posting.Commitment), "internship") {
			offerType = "Internship"
			if !ambiguous && (level == "" || level == "Intern") {
				level = "Intern"
			} else {
				level = ""
			}
		}
		jobs = append(jobs, Job{
			Title: posting.Position, Company: posting.Employer, Description: plainHTML(posting.Description),
			URL: "https://jobs.lever.co/" + site + "/" + id, ApplyURL: applyURL, SourceURL: source,
			Location: leverLocation(posting.Location), WorkMode: leverWorkMode(posting.Location), Type: offerType, Level: level,
			Direction: recognized(posting.Category, []string{"Backend", "Frontend", "Fullstack", "AI/ML/DS", "Data Engineering", "DevOps", "QA Automation"}),
		})
	}
	if len(jobs) == 0 {
		if targetID != "" {
			return nil, errors.New("Lever posting is missing from the public feed; source requires attention")
		}
		return nil, errors.New("Lever feed contains no public postings; source requires attention")
	}
	if problem {
		return jobs, errors.New("some Lever postings are incomplete; source requires attention")
	}
	return jobs, nil
}

func leverLocation(raw string) string {
	var cities []string
	country := false
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == '—' || r == '–' || r == '(' || r == ')'
	}) {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "kazan", "казань":
			cities = append(cities, "Казань")
		case "innopolis", "иннополис":
			cities = append(cities, "Иннополис")
		case "russia", "russian federation", "ru", "рф", "россия", "российская федерация":
			country = true
		}
	}
	if len(cities) == 1 {
		return cities[0]
	}
	if len(cities) > 1 {
		return ""
	}
	if country {
		return "Россия"
	}
	return strings.TrimSpace(raw)
}

func leverWorkMode(raw string) string {
	mode := ""
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",;|—–()/", r)
	}) {
		observed := ""
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "remote", "удаленно", "удалённо":
			observed = "remote"
		case "hybrid", "гибрид":
			observed = "hybrid"
		case "office", "onsite", "on-site", "офис", "очно":
			observed = "office"
		}
		if observed != "" {
			if mode != "" && mode != observed {
				return ""
			}
			mode = observed
		}
	}
	return mode
}

func plainHTML(raw string) string {
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
			if value := strings.Join(strings.Fields(n.Data), " "); value != "" {
				parts = append(parts, value)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return strings.Join(parts, " ")
}
