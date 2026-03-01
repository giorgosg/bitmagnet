package tmdb

import (
	"context"
	"fmt"
	"time"

	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	cacheSize = 1000
	cacheTTL  = time.Hour
)

// clientCached wraps a Client with an in-process LRU cache for detail and search
// requests. This avoids redundant TMDB API calls when many torrents in the same
// classification batch reference the same movie or TV show.
type clientCached struct {
	inner         Client
	movieDetails  *lru.LRU[int64, MovieDetailsResponse]
	tvDetails     *lru.LRU[int64, TvDetailsResponse]
	searchMovie   *lru.LRU[string, SearchMovieResponse]
	searchTv      *lru.LRU[string, SearchTvResponse]
	findByID      *lru.LRU[string, FindByIDResponse]
}

func newCachedClient(inner Client) Client {
	return &clientCached{
		inner:        inner,
		movieDetails: lru.NewLRU[int64, MovieDetailsResponse](cacheSize, nil, cacheTTL),
		tvDetails:    lru.NewLRU[int64, TvDetailsResponse](cacheSize, nil, cacheTTL),
		searchMovie:  lru.NewLRU[string, SearchMovieResponse](cacheSize, nil, cacheTTL),
		searchTv:     lru.NewLRU[string, SearchTvResponse](cacheSize, nil, cacheTTL),
		findByID:     lru.NewLRU[string, FindByIDResponse](cacheSize, nil, cacheTTL),
	}
}

func (c *clientCached) ValidateAPIKey(ctx context.Context) error {
	return c.inner.ValidateAPIKey(ctx)
}

func (c *clientCached) SearchMovie(ctx context.Context, req SearchMovieRequest) (SearchMovieResponse, error) {
	key := fmt.Sprintf("%s|%v|%s|%s", req.Query, req.IncludeAdult, req.Year, req.PrimaryReleaseYear)
	if v, ok := c.searchMovie.Get(key); ok {
		return v, nil
	}

	resp, err := c.inner.SearchMovie(ctx, req)
	if err != nil {
		return resp, err
	}

	c.searchMovie.Add(key, resp)

	return resp, nil
}

func (c *clientCached) MovieDetails(ctx context.Context, req MovieDetailsRequest) (MovieDetailsResponse, error) {
	if v, ok := c.movieDetails.Get(req.ID); ok {
		return v, nil
	}

	resp, err := c.inner.MovieDetails(ctx, req)
	if err != nil {
		return resp, err
	}

	c.movieDetails.Add(req.ID, resp)

	return resp, nil
}

func (c *clientCached) SearchTv(ctx context.Context, req SearchTvRequest) (SearchTvResponse, error) {
	key := fmt.Sprintf("%s|%v|%s|%s", req.Query, req.IncludeAdult, req.Year, req.FirstAirDateYear)
	if v, ok := c.searchTv.Get(key); ok {
		return v, nil
	}

	resp, err := c.inner.SearchTv(ctx, req)
	if err != nil {
		return resp, err
	}

	c.searchTv.Add(key, resp)

	return resp, nil
}

func (c *clientCached) TvDetails(ctx context.Context, req TvDetailsRequest) (TvDetailsResponse, error) {
	if v, ok := c.tvDetails.Get(req.SeriesID); ok {
		return v, nil
	}

	resp, err := c.inner.TvDetails(ctx, req)
	if err != nil {
		return resp, err
	}

	c.tvDetails.Add(req.SeriesID, resp)

	return resp, nil
}

func (c *clientCached) FindByID(ctx context.Context, req FindByIDRequest) (FindByIDResponse, error) {
	key := fmt.Sprintf("%s|%s", req.ExternalSource, req.ExternalID)
	if v, ok := c.findByID.Get(key); ok {
		return v, nil
	}

	resp, err := c.inner.FindByID(ctx, req)
	if err != nil {
		return resp, err
	}

	c.findByID.Add(key, resp)

	return resp, nil
}
