package collector

import (
	"net/url"
	"strings"
)

// PostingURL identifies individual postings on supported ATS hosts, never generic application forms.
func PostingURL(raw string) string {
	if endpoint := GreenhouseClosureEvidenceURL(raw); endpoint != "" {
		return "https://boards.greenhouse.io/" + strings.TrimPrefix(endpoint, "https://boards-api.greenhouse.io/v1/boards/")
	}
	if posting := LeverClosureEvidenceURL(raw); posting != "" {
		return posting
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "jobs.ashbyhq.com" || u.User != nil || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || !leverSite.MatchString(parts[0]) || !leverPostingID.MatchString(parts[1]) || len(parts) == 3 && parts[2] != "application" {
		return ""
	}
	return "https://jobs.ashbyhq.com/" + parts[0] + "/" + strings.ToLower(parts[1])
}
