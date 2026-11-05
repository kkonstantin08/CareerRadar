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
	Closed            bool
	Title             string
	Company           string
	Description       string
	Deadline          string
	URL               string
	ApplyURL          string
	SourceURL         string
	Location          string
	LocationUncertain bool
	WorkMode          string
	Type              string
	Level             string
	Direction         string
	Language          string
	Hours             *int
	Salary            *int64
	SalaryMin         *int64
	SalaryMax         *int64
	Currency          string
}

var weeklyHours = regexp.MustCompile(`(?i)^\s*(\d{1,3})\s*(?:hours? per week|hours? a week|час(?:ов|а)? в неделю)\s*$`)
var levelInTitle = regexp.MustCompile(`(?i)\b(intern|junior|senior)\b`)
var internshipTitle = regexp.MustCompile(`(?i)\b(intern|internship)\b`)
var graduateProgramTitle = regexp.MustCompile(`(?i)\b(trainee|graduate\s+program(?:me)?)\b`)
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
			var locations []string
			for _, place := range schemaObjects(v["jobLocation"]) {
				if addr, ok := place["address"].(map[string]any); ok {
					value, _ := addr["addressLocality"].(string)
					if strings.TrimSpace(value) == "" {
						value, _ = addr["addressCountry"].(string)
					}
					if value = strings.TrimSpace(value); value != "" && !contains(locations, value) {
						locations = append(locations, value)
					}
				}
			}
			// Selecting one of several published locations would silently narrow the listing's eligibility.
			if len(locations) == 1 {
				location = locations[0]
			}
			if strings.EqualFold(str("jobLocationType"), "TELECOMMUTE") {
				mode = "remote"
				areas := schemaObjects(v["applicantLocationRequirements"])
				if len(areas) == 1 && areas[0]["@type"] == "Country" {
					area := areas[0]
					if country, ok := area["name"].(string); ok && strings.TrimSpace(country) != "" {
						location = country
					}
				}
			}
			offerType := titleType(str("title"))
			level, ambiguous := titleLevel(str("title"))
			if schemaContains(v["employmentType"], "INTERN") {
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
			var salaryMin, salaryMax *int64
			currency := ""
			if base, ok := v["baseSalary"].(map[string]any); ok {
				c, _ := base["currency"].(string)
				if value, ok := base["value"].(map[string]any); ok {
					unit, _ := value["unitText"].(string)
					if strings.EqualFold(unit, "MONTH") && c != "" {
						amount := salaryAmount(value["value"])
						lo, hi := salaryAmount(value["minValue"]), salaryAmount(value["maxValue"])
						hasAmount := value["value"] != nil
						hasMin := value["minValue"] != nil
						hasMax := value["maxValue"] != nil
						if amount != nil && !hasMin && !hasMax {
							salary, currency = amount, c
						}
						if !hasAmount && (lo != nil || hi != nil) && (!hasMin || lo != nil) && (!hasMax || hi != nil) && (lo == nil || hi == nil || *lo <= *hi) {
							salaryMin, salaryMax, currency = lo, hi, c
						}
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
			result = append(result, Job{Direction: direction, Language: language, Hours: hours, Salary: salary, SalaryMin: salaryMin, SalaryMax: salaryMax, Currency: currency, Type: offerType, Level: level, Location: location, WorkMode: mode, Title: str("title"), Company: company, Description: str("description"), Deadline: str("validThrough"), URL: str("url"), SourceURL: sourceURL})
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

func salaryAmount(value any) *int64 {
	n, ok := value.(float64)
	if !ok || n < 0 || n > (1<<53)-1 || n != math.Trunc(n) {
		return nil
	}
	amount := int64(n)
	return &amount
}

func schemaObjects(value any) []map[string]any {
	switch value := value.(type) {
	case map[string]any:
		return []map[string]any{value}
	case []any:
		objects := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if object, ok := item.(map[string]any); ok {
				objects = append(objects, object)
			}
		}
		return objects
	default:
		return nil
	}
}

func schemaContains(value any, wanted string) bool {
	switch value := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(value), wanted)
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(text), wanted) {
				return true
			}
		}
	}
	return false
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

func titleType(title string) string {
	if internshipTitle.MatchString(title) {
		return "Internship"
	}
	if graduateProgramTitle.MatchString(title) {
		return "Trainee / Graduate Program"
	}
	return ""
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
