package discovery

import (
	"context"
	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"os"
	"testing"
)

func TestPublicCompanySmoke(t *testing.T) {
	company := os.Getenv("SMOKE_COMPANY")
	if company == "" {
		t.Skip("SMOKE_COMPANY not set")
	}
	results, err := Company(context.Background(), collector.Client(), company)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range results {
		if len(c.CareerURLs) > 0 {
			t.Logf("found %d career links for %s", len(c.CareerURLs), c.Name)
			return
		}
	}
	t.Fatalf("no career link found in %d candidates", len(results))
}

func TestPublicSearchSmoke(t *testing.T) {
	query := os.Getenv("SMOKE_QUERY")
	if query == "" {
		t.Skip("SMOKE_QUERY not set")
	}
	results, err := Search(context.Background(), collector.Client(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no search results")
	}
	t.Logf("received %d attributed results", len(results))
}
