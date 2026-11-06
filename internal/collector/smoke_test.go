package collector

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestPublicSourceSmoke(t *testing.T) {
	source := os.Getenv("SMOKE_SOURCE_URL")
	if source == "" {
		t.Skip("SMOKE_SOURCE_URL not set")
	}
	jobs, err := Fetch(context.Background(), Client(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 || jobs[0].Title == "" {
		t.Fatal("no titled jobs")
	}
	t.Logf("parsed %d opportunity records", len(jobs))
	j := jobs[0]
	facts, err := json.Marshal(map[string]any{"title": j.Title, "company": j.Company, "level": j.Level, "location": j.Location, "work_mode": j.WorkMode, "currency": j.Currency, "salary_min": j.SalaryMin, "salary_max": j.SalaryMax, "deadline": j.Deadline})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first observation: %s", facts)
}

func TestPublicHabrArchiveSmoke(t *testing.T) {
	source := os.Getenv("SMOKE_HABR_ARCHIVED_URL")
	if source == "" {
		t.Skip("SMOKE_HABR_ARCHIVED_URL not set")
	}
	got, err := Fetch(context.Background(), Client(), source)
	if err != nil || len(got) != 1 || !got[0].Closed || got[0].URL != source || got[0].SourceURL != source {
		t.Fatalf("archive=%+v err=%v", got, err)
	}
}
