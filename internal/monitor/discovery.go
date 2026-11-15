package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/kkonstantin08/CareerRadar/internal/discovery"
)

// ErrDiscoveryUnavailable indicates that the public search source could not be read.
var ErrDiscoveryUnavailable = errors.New("global discovery unavailable")

// Discover searches public vacancies and saves observations under the shared Habr gate.
func (s *Service) Discover(ctx context.Context, query string, client *http.Client) ([]discovery.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := s.LockHabr(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	results, err := discovery.Search(ctx, client, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDiscoveryUnavailable, err)
	}
	if err = s.db.SaveDiscovered(ctx, results); err != nil {
		return nil, err
	}
	return results, nil
}
