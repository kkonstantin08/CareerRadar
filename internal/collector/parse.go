// Package collector extracts explicitly published opportunity information.
package collector

import (
	"encoding/json"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// Job is a structured opportunity and the page on which it was observed.
type Job struct {
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
}

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
			typ, _ := v["@type"].(string)
			if typ != "JobPosting" {
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
			}
			offerType, level := "", ""
			if strings.EqualFold(str("employmentType"), "INTERN") {
				offerType = "Internship"
				level = "Intern"
			}
			result = append(result, Job{Type: offerType, Level: level, Location: location, WorkMode: mode, Title: str("title"), Company: company, Description: str("description"), Deadline: str("validThrough"), URL: str("url"), SourceURL: sourceURL})
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
