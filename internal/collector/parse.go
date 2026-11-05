// Package collector extracts explicitly published opportunity information.
package collector

import (
	"encoding/json"
	"io"
	"math"
	"net/url"
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
var levelInTitle = regexp.MustCompile(`(?i)\b(intern|junior|senior)\b|(?:^|[^\pL\pN])(?:стаж[её]р|джун(?:иор)?|сеньор|сениор|младш(?:ий|ая|ее|ие)|старш(?:ий|ая|ее|ие))(?:$|[^\pL\pN])`)
var internshipTitle = regexp.MustCompile(`(?i)\b(intern|internship)\b|(?:^|[^\pL\pN])стаж[её]р(?:$|[^\pL\pN])|(?:^|[^\pL\pN])стажировка(?:$|[^\pL\pN])`)
var graduateProgramTitle = regexp.MustCompile(`(?i)\b(trainee|graduate\s+program(?:me)?)\b|(?:^|[^\pL\pN])программа\s+(?:для\s+)?выпускников(?:$|[^\pL\pN])`)
var educationProgramTitle = regexp.MustCompile(`(?i)\b(?:boot\s?camp|educational\s+program(?:me)?|education\s+program(?:me)?)\b|(?:^|[^\pL\pN])(?:буткемп|образовательная\s+программа|обучающая\s+программа)(?:$|[^\pL\pN])`)
var middleRole = regexp.MustCompile(`(?i)\bmiddle[\s-]+(?:(?:go|python|java|backend|frontend|fullstack|software|qa|devops)[\s-]+)?(?:developer|engineer|programmer)\b|(?:^|[^\pL\pN])мидл[\s-]+(?:(?:go|python|java|бэкенд|фронтенд|backend|frontend)[\s-]+)?(?:разработчик|инженер|программист|тестировщик|специалист)(?:$|[^\pL\pN])`)
var mixedMiddle = regexp.MustCompile(`(?i)\bmiddle(?:\s*[-/|–—]\s*|\s+to\s+)(?:junior|senior)\b|(?:^|[^\pL\pN])мидл\s*[-/|–—]\s*(?:джун|сеньор)(?:$|[^\pL\pN])`)

// Parse extracts supported schema.org job and education program records from HTML without inferring facts.
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
			posting := schemaTypeContains(v["@type"], "JobPosting")
			program := schemaTypeContains(v["@type"], "EducationalOccupationalProgram")
			if !posting && !program {
				return
			}
			str := func(key string) string { s, _ := v[key].(string); return s }
			company := ""
			organization, _ := v["hiringOrganization"].(map[string]any)
			if program {
				organization, _ = v["provider"].(map[string]any)
			}
			if org := organization; org != nil {
				company, _ = org["name"].(string)
			}
			location, mode := "", ""
			var locations []string
			for _, place := range schemaObjects(v["jobLocation"]) {
				if addr, ok := place["address"].(map[string]any); ok {
					value := schemaText(addr["addressLocality"])
					if strings.TrimSpace(value) == "" {
						value = schemaText(addr["addressCountry"])
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
				if len(areas) == 1 && schemaTypeContains(areas[0]["@type"], "Country") {
					area := areas[0]
					if country := schemaText(area["name"]); country != "" {
						location = country
					}
				}
			}
			title := str("title")
			deadline := str("validThrough")
			description := str("description")
			if program {
				title = str("name")
				deadline = str("applicationDeadline")
				mode = programWorkMode(str("educationalProgramMode"))
			}
			requirements := []struct {
				label string
				value any
			}{{"Qualifications", v["qualifications"]}, {"Education requirements", v["educationRequirements"]}, {"Experience requirements", v["experienceRequirements"]}}
			if program {
				requirements = []struct {
					label string
					value any
				}{{"Prerequisites", v["programPrerequisites"]}}
			}
			for _, requirement := range requirements {
				value := schemaText(requirement.value)
				if value != "" && !strings.Contains(strings.ToLower(description), strings.ToLower(value)) {
					description = strings.TrimSpace(description + "\n\n" + requirement.label + ": " + value)
				}
			}
			offerType := titleType(title)
			if program {
				offerType = "Education / Bootcamp"
			}
			level, ambiguous := titleLevel(title)
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
			result = append(result, Job{Direction: direction, Language: language, Hours: hours, Salary: salary, SalaryMin: salaryMin, SalaryMax: salaryMax, Currency: currency, Type: offerType, Level: level, Location: location, WorkMode: mode, Title: title, Company: company, Description: description, Deadline: deadline, URL: str("url"), SourceURL: sourceURL})
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
	if len(result) == 0 {
		var walkMicrodata func(*html.Node)
		walkMicrodata = func(n *html.Node) {
			if n.Type == html.ElementNode && isOpportunityItem(n) {
				// Prefer JSON-LD when a page publishes both formats to avoid duplicate observations.
				if value := microdataItem(n); value != nil {
					extract(value)
				}
				return
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walkMicrodata(child)
			}
		}
		walkMicrodata(doc)
	}
	return result, nil
}

func isOpportunityItem(n *html.Node) bool {
	if !hasAttribute(n, "itemscope") {
		return false
	}
	for _, raw := range strings.Fields(attribute(n, "itemtype")) {
		name := schemaTypeName(raw)
		if name == "JobPosting" || name == "EducationalOccupationalProgram" {
			return true
		}
	}
	return false
}

func programWorkMode(value string) string {
	switch strings.ToLower(strings.Join(strings.Fields(value), " ")) {
	case "online", "remote", "distance learning":
		return "remote"
	case "onsite", "on-site", "in person", "in-person":
		return "office"
	case "blended", "hybrid":
		return "hybrid"
	default:
		return ""
	}
}

// shortcut: itemref is not followed; support it if a real monitored page places Microdata properties outside their item scope.
func microdataItem(n *html.Node) map[string]any {
	var typeName string
	for _, raw := range strings.Fields(attribute(n, "itemtype")) {
		name := schemaTypeName(raw)
		if name == "JobPosting" || name == "EducationalOccupationalProgram" {
			typeName = name
			break
		}
	}
	item := map[string]any{"@type": typeName}
	properties := make(map[string][]any)
	var collect func(*html.Node)
	collect = func(parent *html.Node) {
		for child := parent.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			if hasAttribute(child, "itemscope") {
				if names := strings.Fields(attribute(child, "itemprop")); len(names) > 0 {
					value := any(microdataItem(child))
					for _, name := range names {
						properties[name] = append(properties[name], value)
					}
				}
				continue
			}
			if names := strings.Fields(attribute(child, "itemprop")); len(names) > 0 {
				for _, name := range names {
					properties[name] = append(properties[name], microdataValue(child, name))
				}
				continue
			}
			collect(child)
		}
	}
	collect(n)
	for name, values := range properties {
		if len(values) == 1 {
			item[name] = values[0]
		} else {
			item[name] = values
		}
	}
	return item
}

func schemaTypeContains(value any, wanted string) bool {
	switch value := value.(type) {
	case string:
		return strings.EqualFold(schemaTypeName(value), wanted)
	case []any:
		for _, item := range value {
			if raw, ok := item.(string); ok && strings.EqualFold(schemaTypeName(raw), wanted) {
				return true
			}
		}
	}
	return false
}

func microdataValue(n *html.Node, property string) any {
	for _, key := range []string{"content", "datetime", "href", "src", "value", "data"} {
		if value := attribute(n, key); value != "" {
			if property == "value" || property == "minValue" || property == "maxValue" {
				if number, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					return number
				}
			}
			return strings.TrimSpace(value)
		}
	}
	value := strings.Join(strings.Fields(textContent(n)), " ")
	if property == "value" || property == "minValue" || property == "maxValue" {
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return number
		}
	}
	return value
}

func schemaTypeName(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Hostname() != "schema.org" && u.Hostname() != "www.schema.org") || u.User != nil || u.Port() != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 1 {
			return parts[0]
		}
		return ""
	}
	if strings.ContainsAny(raw, ":/?#") {
		return ""
	}
	return raw
}

func schemaText(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case []any:
		var parts []string
		for _, item := range value {
			if text := schemaText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		if months, ok := value["monthsOfExperience"]; ok {
			if text := schemaText(months); text != "" {
				return text + " months of experience"
			}
		}
		for _, key := range []string{"credentialCategory", "description", "name", "text"} {
			if text := schemaText(value[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func hasAttribute(n *html.Node, name string) bool {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, name) {
			return true
		}
	}
	return false
}

func attribute(n *html.Node, name string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val
		}
	}
	return ""
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
		found := titleLevelName(match)
		if level != "" && level != found {
			return "", true
		}
		level = found
	}
	return level, false
}

func titleLevelName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "стажер", "стажёр":
		return "Intern"
	case "младший", "младшая", "младшее", "младшие", "джун", "джуниор":
		return "Junior"
	case "старший", "старшая", "старшее", "старшие", "сеньор", "сениор":
		return "Senior"
	default:
		return recognized(value, []string{"Intern", "Junior", "Middle", "Senior"})
	}
}

func titleType(title string) string {
	internship := internshipTitle.MatchString(title)
	graduate := graduateProgramTitle.MatchString(title)
	education := educationProgramTitle.MatchString(title)
	if internship && graduate || internship && education || graduate && education {
		return ""
	}
	if internship {
		return "Internship"
	}
	if graduate {
		return "Trainee / Graduate Program"
	}
	if education {
		return "Education / Bootcamp"
	}
	return "Job"
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
