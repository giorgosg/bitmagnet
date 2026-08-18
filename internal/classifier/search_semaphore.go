package classifier

import (
	"context"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

type localSearchSemaphore struct {
	search    LocalSearch
	semaphore chan struct{}
}

// newLocalSearchSemaphore is adapted from ghobs91/lodestone@4785219e4.
// Retain the former single-search behavior for non-positive configuration.
func newLocalSearchSemaphore(search LocalSearch, concurrency int) localSearchSemaphore {
	if concurrency <= 0 {
		concurrency = 1
	}

	return localSearchSemaphore{
		search:    search,
		semaphore: make(chan struct{}, concurrency),
	}
}

func (s localSearchSemaphore) ContentByID(ctx context.Context, ref model.ContentRef) (model.Content, error) {
	select {
	case <-ctx.Done():
		return model.Content{}, ctx.Err()
	case s.semaphore <- struct{}{}:
	}

	defer func() { <-s.semaphore }()

	return s.search.ContentByID(ctx, ref)
}

func (s localSearchSemaphore) ContentBySearch(
	ctx context.Context,
	ct model.ContentType,
	baseTitle string,
	year model.Year,
) (model.Content, error) {
	select {
	case <-ctx.Done():
		return model.Content{}, ctx.Err()
	case s.semaphore <- struct{}{}:
	}

	defer func() { <-s.semaphore }()

	return s.search.ContentBySearch(ctx, ct, baseTitle, year)
}
