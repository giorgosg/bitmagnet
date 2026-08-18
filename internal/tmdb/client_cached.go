package tmdb

import (
	"context"
	"strings"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	cacheSize = 1000
	cacheTTL  = time.Hour
)

// detailsKey identifies a details request. MovieDetailsRequest and
// TvDetailsRequest carry a []string and so cannot be map keys themselves;
// AppendToResponse is joined with the same separator the client uses to build
// the query parameter, so two requests share an entry exactly when they would
// produce the same call to TMDB.
type detailsKey struct {
	id               int64
	appendToResponse string
	language         model.NullString
}

func newDetailsKey(id int64, appendToResponse []string, language model.NullString) detailsKey {
	return detailsKey{
		id:               id,
		appendToResponse: strings.Join(appendToResponse, ","),
		language:         language,
	}
}

// clientCached wraps a Client with an in-process LRU cache for detail and search
// requests. This avoids redundant TMDB API calls when many torrents in the same
// classification batch reference the same movie or TV show.
//
// Cache keys are whole requests. Every field of a request changes the response —
// Language and Region select a localisation, AppendToResponse selects which
// sub-resources are returned at all — so keying on a subset would serve one
// request the answer to a different one.
type clientCached struct {
	inner        Client
	movieDetails *lru.LRU[detailsKey, MovieDetailsResponse]
	tvDetails    *lru.LRU[detailsKey, TvDetailsResponse]
	searchMovie  *lru.LRU[SearchMovieRequest, SearchMovieResponse]
	searchTv     *lru.LRU[SearchTvRequest, SearchTvResponse]
	findByID     *lru.LRU[FindByIDRequest, FindByIDResponse]
}

func newCachedClient(inner Client) Client {
	return &clientCached{
		inner:        inner,
		movieDetails: lru.NewLRU[detailsKey, MovieDetailsResponse](cacheSize, nil, cacheTTL),
		tvDetails:    lru.NewLRU[detailsKey, TvDetailsResponse](cacheSize, nil, cacheTTL),
		searchMovie:  lru.NewLRU[SearchMovieRequest, SearchMovieResponse](cacheSize, nil, cacheTTL),
		searchTv:     lru.NewLRU[SearchTvRequest, SearchTvResponse](cacheSize, nil, cacheTTL),
		findByID:     lru.NewLRU[FindByIDRequest, FindByIDResponse](cacheSize, nil, cacheTTL),
	}
}

func (c *clientCached) ValidateAPIKey(ctx context.Context) error {
	return c.inner.ValidateAPIKey(ctx)
}

func (c *clientCached) SearchMovie(ctx context.Context, req SearchMovieRequest) (SearchMovieResponse, error) {
	if v, ok := c.searchMovie.Get(req); ok {
		return v, nil
	}

	resp, err := c.inner.SearchMovie(ctx, req)
	if err != nil {
		return resp, err
	}

	c.searchMovie.Add(req, resp)

	return resp, nil
}

func (c *clientCached) MovieDetails(ctx context.Context, req MovieDetailsRequest) (MovieDetailsResponse, error) {
	key := newDetailsKey(req.ID, req.AppendToResponse, req.Language)
	if v, ok := c.movieDetails.Get(key); ok {
		return v, nil
	}

	resp, err := c.inner.MovieDetails(ctx, req)
	if err != nil {
		return resp, err
	}

	c.movieDetails.Add(key, resp)

	return resp, nil
}

func (c *clientCached) SearchTv(ctx context.Context, req SearchTvRequest) (SearchTvResponse, error) {
	if v, ok := c.searchTv.Get(req); ok {
		return v, nil
	}

	resp, err := c.inner.SearchTv(ctx, req)
	if err != nil {
		return resp, err
	}

	c.searchTv.Add(req, resp)

	return resp, nil
}

func (c *clientCached) TvDetails(ctx context.Context, req TvDetailsRequest) (TvDetailsResponse, error) {
	key := newDetailsKey(req.SeriesID, req.AppendToResponse, req.Language)
	if v, ok := c.tvDetails.Get(key); ok {
		return v, nil
	}

	resp, err := c.inner.TvDetails(ctx, req)
	if err != nil {
		return resp, err
	}

	c.tvDetails.Add(key, resp)

	return resp, nil
}

func (c *clientCached) FindByID(ctx context.Context, req FindByIDRequest) (FindByIDResponse, error) {
	if v, ok := c.findByID.Get(req); ok {
		return v, nil
	}

	resp, err := c.inner.FindByID(ctx, req)
	if err != nil {
		return resp, err
	}

	c.findByID.Add(req, resp)

	return resp, nil
}
