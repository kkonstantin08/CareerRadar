package collector

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func ashbyURL(u *url.URL) string {
	if u.Scheme != "https" || u.Host != "jobs.ashbyhq.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	board := strings.Trim(u.Path, "/")
	if board == "" {
		return ""
	}
	for _, r := range board {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return "https://api.ashbyhq.com/posting-api/job-board/" + board
}

func parseAshby(body []byte, source string) ([]Job, error) {
	var data struct {
		Jobs []struct {
			Title       string `json:"title"`
			Description string `json:"descriptionPlain"`
			URL         string `json:"jobUrl"`
			Workplace   string `json:"workplaceType"`
			Employment  string `json:"employmentType"`
			Listed      *bool  `json:"isListed"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &data); err != nil || data.Jobs == nil {
		return nil, errors.New("invalid Ashby response; source requires attention")
	}
	if len(data.Jobs) > 1000 {
		return nil, errors.New("too many Ashby jobs; source requires attention")
	}
	var jobs []Job
	problem := false
	for _, item := range data.Jobs {
		if item.Listed == nil {
			problem = true
			continue
		}
		if !*item.Listed {
			continue
		}
		u, err := url.Parse(item.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() != "jobs.ashbyhq.com" || u.User != nil || item.Title == "" {
			problem = true
			continue
		}
		j := Job{Title: item.Title, Description: item.Description, URL: item.URL, SourceURL: source}
		switch item.Workplace {
		case "Remote":
			j.WorkMode = "remote"
		case "Hybrid":
			j.WorkMode = "hybrid"
		case "OnSite":
			j.WorkMode = "office"
		}
		if item.Employment == "Intern" {
			j.Type = "Internship"
			j.Level = "Intern"
		}
		jobs = append(jobs, j)
	}
	if problem {
		return jobs, errors.New("incomplete Ashby listings; source requires attention")
	}
	if len(jobs) == 0 {
		return nil, errors.New("no listed Ashby vacancies; source requires attention")
	}
	return jobs, nil
}
