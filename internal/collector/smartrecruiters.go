package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var smartPostingID = regexp.MustCompile(`^[0-9]{1,20}$`)

// SmartRecruitersBoard recognizes public company board links.
func SmartRecruitersBoard(u *url.URL) bool {
	company, id := smartRecruitersPath(u)
	return company != "" && id == ""
}

func smartRecruitersPath(u *url.URL) (company, id string) {
	if u.Scheme != "https" || u.User != nil || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "jobs.smartrecruiters.com" && host != "careers.smartrecruiters.com" {
		return "", ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if !leverSite.MatchString(parts[0]) {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	if len(parts) == 2 && host == "jobs.smartrecruiters.com" {
		id, _, _ := strings.Cut(parts[1], "-")
		if smartPostingID.MatchString(id) {
			return parts[0], id
		}
	}
	return "", ""
}

func fetchSmartRecruiters(ctx context.Context, client *http.Client, company, id string) ([]Job, error) {
	api := "https://api.smartrecruiters.com/v1/companies/" + company + "/postings"
	if id != "" {
		job, err := smartRecruitersJob(ctx, client, api, company, id)
		if job.URL == "" {
			return nil, err
		}
		return []Job{job}, err
	}
	var found []Job
	var problem error
	seen := make(map[string]bool)
	offset := 0
	// At most three list pages and fifteen detail calls per source check.
	for range 3 {
		var page struct {
			Offset     *int
			TotalFound *int
			Content    []struct{ ID, Visibility string }
		}
		endpoint := fmt.Sprintf("%s?destination=PUBLIC&limit=5&offset=%d", api, offset)
		if err := readSmartRecruiters(ctx, client, endpoint, &page); err != nil {
			return found, err
		}
		if page.Offset == nil || *page.Offset != offset || page.TotalFound == nil || *page.TotalFound < 0 || page.Content == nil || len(page.Content) > 5 {
			return found, errors.New("invalid SmartRecruiters posting list")
		}
		for _, row := range page.Content {
			if err := ctx.Err(); err != nil {
				return found, err
			}
			if !smartPostingID.MatchString(row.ID) || !smartRecruitersPublic(row.Visibility) || seen[row.ID] {
				problem = errors.New("SmartRecruiters posting list contains missing or conflicting evidence")
				continue
			}
			seen[row.ID] = true
			job, err := smartRecruitersJob(ctx, client, api, company, row.ID)
			if err != nil {
				problem = err
			}
			if job.URL != "" {
				found = append(found, job)
			}
		}
		offset += len(page.Content)
		if offset >= *page.TotalFound {
			if len(found) == 0 && problem == nil {
				problem = errors.New("no verified SmartRecruiters vacancies found")
			}
			return found, problem
		}
		if len(page.Content) == 0 {
			return found, errors.New("incomplete SmartRecruiters posting list")
		}
	}
	return found, errors.Join(problem, errors.New("SmartRecruiters posting page limit reached"))
}

func readSmartRecruiters(ctx context.Context, client *http.Client, endpoint string, target any) error {
	body, actual, err := readPage(ctx, client, endpoint)
	if err != nil {
		return err
	}
	if actual != endpoint {
		return errors.New("unexpected SmartRecruiters API redirect")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid SmartRecruiters response: %w", err)
	}
	return nil
}

func smartRecruitersJob(ctx context.Context, client *http.Client, api, company, id string) (Job, error) {
	var row struct {
		ID, Name, Visibility, PostingURL, ApplyURL string
		Active                                     bool
		Company                                    struct{ Identifier, Name string }
		Location                                   struct {
			City           string
			Remote, Hybrid bool
		}
		ExperienceLevel struct{ ID string }
		JobAd           struct {
			Sections struct {
				JobDescription, Qualifications, AdditionalInformation struct{ Text string }
			}
		}
	}
	endpoint := api + "/" + id
	if err := readSmartRecruiters(ctx, client, endpoint, &row); err != nil {
		return Job{}, err
	}
	if row.ID != id || !row.Active || !smartRecruitersPublic(row.Visibility) || !strings.EqualFold(row.Company.Identifier, company) || strings.TrimSpace(row.Name) == "" || !smartRecruitersSamePosting(row.PostingURL, company, id) {
		return Job{}, errors.New("could not verify active public SmartRecruiters posting")
	}
	job := Job{Title: row.Name, URL: row.PostingURL, SourceURL: endpoint, Company: row.Company.Name, Location: row.Location.City}
	job.Type, job.Level = TitleFacts(row.Name)
	if row.ExperienceLevel.ID == "internship" {
		job.Type = "Internship"
		level, ambiguous := titleLevel(row.Name)
		if !ambiguous && (level == "" || level == "Intern") {
			job.Level = "Intern"
		} else {
			job.Level = ""
		}
	}
	switch {
	case row.Location.Remote && !row.Location.Hybrid:
		job.WorkMode = "remote"
	case row.Location.Hybrid && !row.Location.Remote:
		job.WorkMode = "hybrid"
	}
	sections := row.JobAd.Sections
	var description []string
	for _, section := range []string{sections.JobDescription.Text, sections.Qualifications.Text, sections.AdditionalInformation.Text} {
		if value := structuredText(strings.TrimSpace(section)); value != "" {
			description = append(description, value)
		}
	}
	job.Description = strings.Join(description, "\n\n")
	if row.ApplyURL != "" {
		if !smartRecruitersSamePosting(row.ApplyURL, company, id) {
			return job, errors.New("unverified SmartRecruiters application link")
		}
		job.ApplyURL = row.ApplyURL
	}
	return job, nil
}

// The API serves unauthenticated public postings even when it omits the visibility field.
func smartRecruitersPublic(visibility string) bool {
	return visibility == "" || visibility == "PUBLIC"
}

func smartRecruitersSamePosting(raw, company, id string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	actualCompany, actualID := smartRecruitersPath(u)
	return actualID == id && strings.EqualFold(actualCompany, company)
}
