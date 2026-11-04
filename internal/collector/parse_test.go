package collector

import (
	"os"
	"strings"
	"testing"
)

func TestParseJobPosting(t *testing.T) {
	f, err := os.Open("testdata/job.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	jobs, err := Parse(f, "https://example.org/jobs/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	if jobs[0].Title != "Junior Go Developer" || jobs[0].Company != "Example Engineering" || jobs[0].SourceURL != "https://example.org/jobs/1" || jobs[0].Deadline != "2026-12-01" {
		t.Fatalf("unexpected job: %+v", jobs[0])
	}
}

func TestParseExplicitLocationAndRemote(t *testing.T) {
	html := `<script type="application/ld+json">{"@type":"JobPosting","title":"Backend Intern","jobLocationType":"TELECOMMUTE","jobLocation":{"address":{"addressLocality":"Казань"}}}</script>`
	jobs, err := Parse(strings.NewReader(html), "https://example.org/job")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	if jobs[0].Location != "Казань" || jobs[0].WorkMode != "remote" {
		t.Fatalf("job %+v", jobs[0])
	}
}

func TestExplicitInternshipType(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Software Intern","employmentType":"INTERN"}</script>`
	jobs, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	if jobs[0].Type != "Internship" || jobs[0].Level != "Intern" {
		t.Fatalf("job %+v", jobs[0])
	}
}

func TestParseExplicitMonthlySalaryAndWeeklyHours(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Intern","workHours":"20 hours per week","baseSalary":{"currency":"RUB","value":{"value":70000,"unitText":"MONTH"}}}</script>`
	got, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(got) != 1 {
		t.Fatalf("jobs: %+v %v", got, err)
	}
	if got[0].Hours == nil || *got[0].Hours != 20 || got[0].Salary == nil || *got[0].Salary != 70000 || got[0].Currency != "RUB" {
		t.Fatalf("fields: %+v", got[0])
	}
}
