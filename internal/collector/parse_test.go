package collector

import (
	"os"
	"reflect"
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

func TestParseLocationCountryWhenCityMissing(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Engineer","jobLocation":{"address":{"@type":"PostalAddress","addressCountry":"RU"}}}</script>`
	got, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(got) != 1 || got[0].Location != "RU" {
		t.Fatalf("jobs %+v %v", got, err)
	}
}

func TestParseRemoteApplicantCountry(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","jobLocationType":"TELECOMMUTE","applicantLocationRequirements":{"@type":"Country","name":"Russia"}}</script>`
	got, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(got) != 1 || got[0].Location != "Russia" || got[0].WorkMode != "remote" {
		t.Fatalf("jobs %+v %v", got, err)
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

func TestTitleExplicitLevelAndAmbiguousLevel(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"Senior Go Developer", "Senior"}, {"Junior backend engineer", "Junior"}, {"Intern Python", "Intern"}, {"Middle Go Developer", "Middle"}, {"Middle / Senior engineer", ""}, {"Middle-Senior Engineer", ""}, {"Middle to Senior Engineer", ""},
		{"Junior / Senior engineer", ""}, {"Seniorist engineer", ""}, {"Middle Office Developer", ""}, {"Middle East Developer", ""}, {"Middle Market Developer", ""}, {"Senior Middle Office Developer", "Senior"},
	} {
		body := `<script type="application/ld+json">{"@type":"JobPosting","title":"` + tc.title + `"}</script>`
		got, err := Parse(strings.NewReader(body), "https://example.org/job")
		if err != nil || len(got) != 1 || got[0].Level != tc.want {
			t.Errorf("%s: %+v %v", tc.title, got, err)
		}
	}
}

func TestConflictingTitleAndInternshipLevelRemainUnknown(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Senior Engineer","employmentType":"INTERN"}</script>`
	got, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(got) != 1 || got[0].Level != "" {
		t.Fatalf("conflicting level: %+v %v", got, err)
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

func TestParseMonthlySalaryRange(t *testing.T) {
	for _, tc := range []struct {
		salary string
		valid  bool
	}{
		{`{"currency":"RUB","value":{"minValue":80000,"maxValue":120000,"unitText":"MONTH"}}`, true},
		{`{"currency":"RUB","value":{"minValue":120000,"maxValue":80000,"unitText":"MONTH"}}`, false},
		{`{"currency":"RUB","value":{"minValue":80000,"maxValue":120000,"unitText":"YEAR"}}`, false},
		{`{"currency":"RUB","value":{"minValue":9007199254740992,"maxValue":9007199254740993,"unitText":"MONTH"}}`, false},
		{`{"currency":"RUB","value":{"value":100000,"minValue":"invalid","unitText":"MONTH"}}`, false},
	} {
		body := `<script type="application/ld+json">{"@type":"JobPosting","baseSalary":` + tc.salary + `}</script>`
		jobs, err := Parse(strings.NewReader(body), "https://example.org/job")
		if err != nil || len(jobs) != 1 {
			t.Fatalf("jobs %+v %v", jobs, err)
		}
		got := jobs[0].SalaryMin != nil && jobs[0].SalaryMax != nil && *jobs[0].SalaryMin == 80000 && *jobs[0].SalaryMax == 120000 && jobs[0].Currency == "RUB"
		if got != tc.valid {
			t.Errorf("range %+v", jobs[0])
		}
		if !tc.valid && (jobs[0].Salary != nil || jobs[0].SalaryMin != nil || jobs[0].SalaryMax != nil || jobs[0].Currency != "") {
			t.Errorf("invalid salary retained: %+v", jobs[0])
		}
	}
}

func TestParsePartialMonthlySalaryRange(t *testing.T) {
	for _, tc := range []struct {
		value           string
		min, max, exact *int64
	}{
		{`{"value":null,"minValue":80000,"maxValue":120000,"unitText":"MONTH"}`, salaryTestAmount(80000), salaryTestAmount(120000), nil},
		{`{"value":100000,"minValue":null,"maxValue":null,"unitText":"MONTH"}`, nil, nil, salaryTestAmount(100000)},
		{`{"minValue":80000,"unitText":"MONTH"}`, salaryTestAmount(80000), nil, nil},
		{`{"maxValue":120000,"unitText":"MONTH"}`, nil, salaryTestAmount(120000), nil},
	} {
		body := `<script type="application/ld+json">{"@type":"JobPosting","baseSalary":{"currency":"RUB","value":` + tc.value + `}}</script>`
		got, err := Parse(strings.NewReader(body), "https://example.org/job")
		if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].SalaryMin, tc.min) || !reflect.DeepEqual(got[0].SalaryMax, tc.max) || !reflect.DeepEqual(got[0].Salary, tc.exact) || got[0].Currency != "RUB" {
			t.Fatalf("salary %+v %v", got, err)
		}
	}
}

func salaryTestAmount(n int64) *int64 { return &n }

func TestParseExplicitSkillAndOccupation(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","title":"Engineer","occupationalCategory":"Backend","skills":["Go"]}</script>`
	found, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(found) != 1 {
		t.Fatalf("jobs %+v %v", found, err)
	}
	if found[0].Direction != "Backend" || found[0].Language != "Go" {
		t.Fatalf("job %+v", found[0])
	}
}

func TestUnknownOccupationalCodeAndSkillAreNotInvented(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","occupationalCategory":"15-1252","skills":["teamwork"]}</script>`
	found, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(found) != 1 {
		t.Fatalf("jobs %+v %v", found, err)
	}
	if found[0].Direction != "" || found[0].Language != "" {
		t.Fatalf("unrecognized fields: %+v", found[0])
	}
}

func TestSkillsExtractOneLanguageAmongOtherSkills(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","skills":["Docker","Go","PostgreSQL"]}</script>`
	found, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(found) != 1 || found[0].Language != "Go" {
		t.Fatalf("jobs %+v %v", found, err)
	}
}
func TestMultipleLanguagesAreRetained(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":"JobPosting","skills":["Go","Python"]}</script>`
	found, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(found) != 1 || found[0].Language != "Go, Python" {
		t.Fatalf("jobs %+v %v", found, err)
	}
}

func TestJobPostingInTypeArray(t *testing.T) {
	body := `<script type="application/ld+json">{"@type":["Thing","JobPosting"],"title":"Engineer"}</script>`
	found, err := Parse(strings.NewReader(body), "https://example.org/job")
	if err != nil || len(found) != 1 || found[0].Title != "Engineer" {
		t.Fatalf("jobs %+v %v", found, err)
	}
}
