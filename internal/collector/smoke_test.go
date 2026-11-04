package collector

import (
	"context"
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
}
