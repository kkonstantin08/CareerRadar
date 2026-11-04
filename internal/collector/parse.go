// Package collector extracts explicitly published opportunity information.
package collector

import (
	"encoding/json"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Job is a structured opportunity and the page on which it was observed.
type Job struct {
	Closed      bool
	Title       string
	Company     string
	Description string
	Deadline    string
	URL         string
	SourceURL   string
	Location    string
	WorkMode    string
	Type        string
	Level       string
	Direction   string
	Language    string
	Hours       *int
	Salary      *int64
	Currency    string
}

var weeklyHours = regexp.MustCompile(`(?i)^\s*(\d{1,3})\s*(?:hours? per week|hours? a week|час(?:ов|а)? в неделю)\s*$`)
var levelInTitle = regexp.MustCompile(`(?i)\b(intern|junior|senior)\b`)
var middleRole = regexp.MustCompile(`(?i)\bmiddle[\s-]+(?:(?:go|python|java|backend|frontend|fullstack|software|qa|devops)[\s-]+)?(?:developer|engineer|programmer)\b`)
var mixedMiddle = regexp.MustCompile(`(?i)\bmiddle(?:\s*[-/|–—]\s*|\s+to\s+)(?:junior|senior)\b`)

// Parse extracts schema.org JobPosting records from HTML without inferring facts.
func Parse(r io.Reader, sourceURL string) ([]Job, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var result []Job
	var extract func(any)
	extract = func(value any) {
		switch v := value.(type) {
		case []any:
			for _, item := range v {
				extract(item)
			}
		case map[string]any:
			if graph, ok := v["@graph"]; ok {
				extract(graph)
			}
			posting := v["@type"] == "JobPosting"
			if types, ok := v["@type"].([]any); ok {
				for _, typ := range types {
					if typ == "JobPosting" {
						posting = true
					}
				}
			}
			if !posting {
				return
			}
			str := func(key string) string { s, _ := v[key].(string); return s }
			company := ""
			if org, ok := v["hiringOrganization"].(map[string]any); ok {
				company, _ = org["name"].(string)
			}
			location, mode := "", ""
			if place, ok := v["jobLocation"].(map[string]any); ok {
				if addr, ok := place["address"].(map[string]any); ok {
					location, _ = addr["addressLocality"].(string)
				}
			}
			if strings.EqualFold(str("jobLocationType"), "TELECOMMUTE") {
				mode = "remote"
				if area, ok := v["applicantLocationRequirements"].(map[string]any); ok && area["@type"] == "Country" {
					if country, ok := area["name"].(string); ok && strings.TrimSpace(country) != "" {
						location = country
					}
				}
			}
			offerType := ""
			level, ambiguous := titleLevel(str("title"))
			if strings.EqualFold(str("employmentType"), "INTERN") {
				offerType = "Internship"
				if !ambiguous && (level == "" || level == "Intern") {
					level = "Intern"
				} else {
					level = ""
				}
			}
			var hours *int
			if m := weeklyHours.FindStringSubmatch(str("workHours")); len(m) == 2 {
				if n, e := strconv.Atoi(m[1]); e == nil && n <= 168 {
					hours = &n
				}
			}
			var salary *int64
			currency := ""
			if base, ok := v["baseSalary"].(map[string]any); ok {
				c, _ := base["currency"].(string)
				if value, ok := base["value"].(map[string]any); ok {
					unit, _ := value["unitText"].(string)
					n, ok := value["value"].(float64)
					if ok && strings.EqualFold(unit, "MONTH") && c != "" && n >= 0 && n <= float64(math.MaxInt64/2) && n == math.Trunc(n) {
						amount := int64(n)
						salary = &amount
						currency = c
					}
				}
			}
			direction := recognized(str("occupationalCategory"), []string{"Backend", "Frontend", "Fullstack", "AI/ML/DS", "Data Engineering", "DevOps", "QA Automation"})
			var languages []string
			values, ok := v["skills"].([]any)
			if !ok {
				if value, ok := v["skills"].(string); ok {
					values = []any{value}
				}
			}
			for _, value := range values {
				raw, _ := value.(string)
				found := recognized(raw, []string{"Go", "Python", "Java", "JavaScript", "TypeScript", "C++", "C#", "Rust", "Kotlin", "Swift", "PHP", "Ruby"})
				if found == "" {
					continue
				}
				if !contains(languages, found) {
					languages = append(languages, found)
				}
			}
			sort.Strings(languages)
			language := strings.Join(languages, ", ")
			result = append(result, Job{Direction: direction, Language: language, Hours: hours, Salary: salary, Currency: currency, Type: offerType, Level: level, Location: location, WorkMode: mode, Title: str("title"), Company: company, Description: str("description"), Deadline: str("validThrough"), URL: str("url"), SourceURL: sourceURL})
		}
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, attr := range n.Attr {
				if attr.Key == "type" && strings.EqualFold(strings.TrimSpace(attr.Val), "application/ld+json") && n.FirstChild != nil {
					var value any
					if json.Unmarshal([]byte(n.FirstChild.Data), &value) == nil {
						extract(value)
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return result, nil
}

func titleLevel(title string) (string, bool) {
	level := ""
	if mixedMiddle.MatchString(title) {
		return "", true
	}
	if middleRole.MatchString(title) {
		level = "Middle"
	}
	for _, match := range levelInTitle.FindAllString(title, -1) {
		found := recognized(match, []string{"Intern", "Junior", "Middle", "Senior"})
		if level != "" && level != found {
			return "", true
		}
		level = found
	}
	return level, false
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func recognized(value string, allowed []string) string {
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), item) {
			return item
		}
	}
	return ""
}
