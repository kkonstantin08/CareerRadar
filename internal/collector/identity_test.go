package collector

import "testing"

func TestPostingURLUsesOnlyIndividualProviderPaths(t *testing.T) {
	const id = "12345678-1234-1234-1234-123456789abc"
	for raw, want := range map[string]string{
		"https://job-boards.greenhouse.io/example/jobs/123?gh_jid=123": "https://boards.greenhouse.io/example/jobs/123",
		"https://jobs.lever.co/example/" + id + "/apply":               "https://jobs.lever.co/example/" + id,
		"https://jobs.ashbyhq.com/example/" + id + "/application":      "https://jobs.ashbyhq.com/example/" + id,
		"https://example.org/apply":                                    "",
		"https://jobs.lever.co/example":                                "",
		"https://jobs.ashbyhq.com/example/application":                 "",
		"https://jobs.ashbyhq.com.evil.test/example/" + id:             "",
		"https://user@jobs.ashbyhq.com/example/" + id:                  "",
		"https://jobs.ashbyhq.com:444/example/" + id:                   "",
	} {
		if got := PostingURL(raw); got != want {
			t.Errorf("%s: got %q want %q", raw, got, want)
		}
	}
}
