package tmdb

import (
	"context"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testQueryMovie     = "arrival"
	testExternalSource = "imdb_id"
)

// countingClient records how many times each method reached the inner client,
// which is the only thing worth asserting about a cache.
type countingClient struct {
	Client

	searchMovie  int
	movieDetails int
	searchTv     int
	tvDetails    int
	findByID     int
}

func (c *countingClient) SearchMovie(context.Context, SearchMovieRequest) (SearchMovieResponse, error) {
	c.searchMovie++

	return SearchMovieResponse{TotalResults: int64(c.searchMovie)}, nil
}

func (c *countingClient) MovieDetails(context.Context, MovieDetailsRequest) (MovieDetailsResponse, error) {
	c.movieDetails++

	return MovieDetailsResponse{ID: int64(c.movieDetails)}, nil
}

func (c *countingClient) SearchTv(context.Context, SearchTvRequest) (SearchTvResponse, error) {
	c.searchTv++

	return SearchTvResponse{TotalResults: int64(c.searchTv)}, nil
}

func (c *countingClient) TvDetails(context.Context, TvDetailsRequest) (TvDetailsResponse, error) {
	c.tvDetails++

	return TvDetailsResponse{ID: int64(c.tvDetails)}, nil
}

func (c *countingClient) FindByID(context.Context, FindByIDRequest) (FindByIDResponse, error) {
	c.findByID++

	return FindByIDResponse{}, nil
}

func TestCachedClientServesRepeatedRequestsFromCache(t *testing.T) {
	t.Parallel()

	inner := &countingClient{}
	c := newCachedClient(inner)
	ctx := context.Background()

	first, err := c.SearchMovie(ctx, SearchMovieRequest{Query: testQueryMovie})
	require.NoError(t, err)

	second, err := c.SearchMovie(ctx, SearchMovieRequest{Query: testQueryMovie})
	require.NoError(t, err)

	assert.Equal(t, 1, inner.searchMovie, "an identical request should not reach the inner client twice")
	assert.Equal(t, first, second)
}

// Every field of a request that changes what TMDB returns must be part of the
// cache key. Language and Region select a localisation; AppendToResponse selects
// which sub-resources come back at all. Keying on a subset means a later request
// silently receives an answer to a different question.
func TestCachedClientKeysOnEveryRequestField(t *testing.T) {
	t.Parallel()

	english := model.NewNullString("en-US")
	german := model.NewNullString("de-DE")

	t.Run("SearchMovie distinguishes language and region", func(t *testing.T) {
		t.Parallel()

		inner := &countingClient{}
		c := newCachedClient(inner)
		ctx := context.Background()

		base := SearchMovieRequest{Query: testQueryMovie}

		withLanguage := base
		withLanguage.Language = german

		withRegion := base
		withRegion.Region = model.NewNullString("DE")

		for _, req := range []SearchMovieRequest{base, withLanguage, withRegion} {
			_, err := c.SearchMovie(ctx, req)
			require.NoError(t, err)
		}

		assert.Equal(
			t,
			3,
			inner.searchMovie,
			"language and region change the response and must be part of the key",
		)
	})

	t.Run("MovieDetails distinguishes appendToResponse and language", func(t *testing.T) {
		t.Parallel()

		inner := &countingClient{}
		c := newCachedClient(inner)
		ctx := context.Background()

		reqs := []MovieDetailsRequest{
			{ID: 42},
			{ID: 42, AppendToResponse: []string{"credits"}},
			{ID: 42, AppendToResponse: []string{"credits", "external_ids"}},
			{ID: 42, Language: english},
		}
		for _, req := range reqs {
			_, err := c.MovieDetails(ctx, req)
			require.NoError(t, err)
		}

		assert.Equal(t, len(reqs), inner.movieDetails,
			"appendToResponse selects which fields TMDB returns; a cache hit here loses data")
	})

	t.Run("SearchTv distinguishes language", func(t *testing.T) {
		t.Parallel()

		inner := &countingClient{}
		c := newCachedClient(inner)
		ctx := context.Background()

		base := SearchTvRequest{Query: "dark"}

		withLanguage := base
		withLanguage.Language = german

		for _, req := range []SearchTvRequest{base, withLanguage} {
			_, err := c.SearchTv(ctx, req)
			require.NoError(t, err)
		}

		assert.Equal(t, 2, inner.searchTv)
	})

	t.Run("TvDetails distinguishes appendToResponse and language", func(t *testing.T) {
		t.Parallel()

		inner := &countingClient{}
		c := newCachedClient(inner)
		ctx := context.Background()

		reqs := []TvDetailsRequest{
			{SeriesID: 7},
			{SeriesID: 7, AppendToResponse: []string{"external_ids"}},
			{SeriesID: 7, Language: english},
		}
		for _, req := range reqs {
			_, err := c.TvDetails(ctx, req)
			require.NoError(t, err)
		}

		assert.Equal(t, len(reqs), inner.tvDetails)
	})

	t.Run("FindByID distinguishes language", func(t *testing.T) {
		t.Parallel()

		inner := &countingClient{}
		c := newCachedClient(inner)
		ctx := context.Background()

		reqs := []FindByIDRequest{
			{ExternalSource: testExternalSource, ExternalID: "tt0000001"},
			{ExternalSource: testExternalSource, ExternalID: "tt0000001", Language: german},
		}
		for _, req := range reqs {
			_, err := c.FindByID(ctx, req)
			require.NoError(t, err)
		}

		assert.Equal(t, len(reqs), inner.findByID)
	})
}

// Distinct queries must not collide, which a naive delimiter-joined key can get
// wrong when a field itself contains the delimiter.
func TestCachedClientDoesNotCollideAcrossDistinctRequests(t *testing.T) {
	t.Parallel()

	inner := &countingClient{}
	c := newCachedClient(inner)
	ctx := context.Background()

	reqs := []SearchMovieRequest{
		{Query: "a|b"},
		{Query: "a", Region: model.NewNullString("b")},
		{Query: "a"},
	}
	for _, req := range reqs {
		_, err := c.SearchMovie(ctx, req)
		require.NoError(t, err)
	}

	assert.Equal(t, len(reqs), inner.searchMovie, "distinct requests must not share a cache entry")
}
