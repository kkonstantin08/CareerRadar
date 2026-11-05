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
	return "https://api.ashbyhq.com/posting-api/job-board/" + board + "?includeCompensation=true"
}

func parseAshby(body []byte, source string) ([]Job, error) {
	var data struct {
		Jobs []struct {
			Title       string `json:"title"`
			Description string `json:"descriptionPlain"`
			URL         string `json:"jobUrl"`
			ApplyURL    string `json:"applyUrl"`
			Location    string `json:"location"`
			Workplace   string `json:"workplaceType"`
			Employment  string `json:"employmentType"`
			Listed      *bool  `json:"isListed"`
			Address     struct {
				PostalAddress struct {
					City    string `json:"addressLocality"`
					Country string `json:"addressCountry"`
				} `json:"postalAddress"`
			} `json:"address"`
			Secondary    []json.RawMessage `json:"secondaryLocations"`
			Compensation struct {
				Summary []struct {
					Type     string   `json:"compensationType"`
					Interval string   `json:"interval"`
					Currency string   `json:"currencyCode"`
					Min      *float64 `json:"minValue"`
					Max      *float64 `json:"maxValue"`
				} `json:"summaryComponents"`
			} `json:"compensation"`
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
		j.Level, _ = titleLevel(item.Title)
		j.Type = titleType(item.Title)
		if item.ApplyURL != "" {
			apply, err := url.Parse(item.ApplyURL)
			if err != nil || apply.Scheme != "https" || apply.Hostname() != "jobs.ashbyhq.com" || apply.User != nil {
				problem = true
			} else {
				j.ApplyURL = apply.String()
			}
		}
		j.LocationUncertain = item.Secondary != nil && len(item.Secondary) > 0
		switch item.Workplace {
		case "Remote":
			j.WorkMode = "remote"
		case "Hybrid":
			j.WorkMode = "hybrid"
		case "OnSite":
			j.WorkMode = "office"
		}
		// A remote or multi-location posting does not establish one eligible city.
		if (j.WorkMode == "office" || j.WorkMode == "hybrid") && item.Secondary != nil && len(item.Secondary) == 0 {
			j.Location = item.Address.PostalAddress.City
			if strings.TrimSpace(j.Location) == "" {
				j.Location = item.Address.PostalAddress.Country
			}
			if strings.TrimSpace(j.Location) == "" {
				j.Location = item.Location
			}
		}
		if item.Employment == "Intern" {
			j.Type = "Internship"
			j.Level = "Intern"
		}
		salaryCount := 0
		var interval, currency string
		var rawMin, rawMax *float64
		for _, component := range item.Compensation.Summary {
			if !strings.EqualFold(component.Type, "Salary") {
				continue
			}
			salaryCount++
			interval, currency, rawMin, rawMax = component.Interval, component.Currency, component.Min, component.Max
		}
		if salaryCount == 1 {
			divisor := int64(0)
			switch strings.ToUpper(strings.TrimSpace(interval)) {
			case "1 MONTH":
				divisor = 1
			case "1 YEAR":
				divisor = 12
			}
			var minimum, maximum *int64
			if rawMin != nil {
				minimum = salaryAmount(*rawMin)
			}
			if rawMax != nil {
				maximum = salaryAmount(*rawMax)
			}
			// The salary filters use monthly values, so normalize only explicit month/year intervals.
			if divisor > 0 && strings.TrimSpace(currency) != "" && (minimum != nil || maximum != nil) && (minimum == nil || maximum == nil || *minimum <= *maximum) {
				if minimum != nil {
					*minimum /= divisor
				}
				if maximum != nil {
					*maximum /= divisor
				}
				if minimum != nil && maximum != nil && *minimum == *maximum {
					j.Salary = minimum
				} else {
					j.SalaryMin, j.SalaryMax = minimum, maximum
				}
				j.Currency = strings.TrimSpace(currency)
			}
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
