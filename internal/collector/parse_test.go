package collector

import (
	"os"
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
