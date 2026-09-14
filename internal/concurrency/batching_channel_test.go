package concurrency_test

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/concurrency"
	"github.com/stretchr/testify/require"
)

// Close is the shutdown path: whatever is buffered has to reach Out, or the
// caller's last batch is silently discarded. A wait time long enough never to
// fire is what makes this about Close rather than about the ticker.
func TestBatchingChannelCloseFlushesWhatIsBuffered(t *testing.T) {
	t.Parallel()

	ch := concurrency.NewBatchingChannel[int](10, 100, time.Hour)
	for i := range 3 {
		ch.In() <- i
	}

	ch.Close()

	select {
	case batch := <-ch.Out():
		require.Equal(t, []int{0, 1, 2}, batch)
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not flush the buffered items")
	}

	select {
	case _, ok := <-ch.Out():
		require.False(t, ok, "Out must be closed once the last batch has been flushed")
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not close Out")
	}
}

// Items sitting on the input channel have not reached the buffer yet, and are
// just as lost if the close ignores them.
func TestBatchingChannelCloseFlushesItemsStillQueued(t *testing.T) {
	t.Parallel()

	// A batch size of 1 would flush on arrival; a wait time of an hour keeps the
	// ticker out of it. So anything that comes out came out of Close.
	ch := concurrency.NewBatchingChannel[int](10, 100, time.Hour)

	// Fill the input without letting the batching goroutine run to completion
	// first: whichever side wins, every item must be accounted for.
	for i := range 5 {
		ch.In() <- i
	}

	ch.Close()

	var got []int

	for batch := range ch.Out() {
		got = append(got, batch...)
	}

	require.Equal(t, []int{0, 1, 2, 3, 4}, got)
}

// A batch must never exceed the size the channel was built with, including the
// one the close produces.
func TestBatchingChannelCloseRespectsTheMaxBatchSize(t *testing.T) {
	t.Parallel()

	ch := concurrency.NewBatchingChannel[int](10, 2, time.Hour)
	for i := range 5 {
		ch.In() <- i
	}

	ch.Close()

	var sizes []int

	var got []int

	for batch := range ch.Out() {
		sizes = append(sizes, len(batch))
		got = append(got, batch...)
	}

	require.Equal(t, []int{0, 1, 2, 3, 4}, got)

	for _, size := range sizes {
		require.LessOrEqual(t, size, 2)
	}
}

// Stopping twice is normal on a shutdown path that can be reached from more
// than one place.
func TestBatchingChannelCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	ch := concurrency.NewBatchingChannel[int](10, 100, time.Hour)
	ch.In() <- 1

	ch.Close()
	ch.Close()

	batch, ok := <-ch.Out()
	require.True(t, ok)
	require.Equal(t, []int{1}, batch)
}
