package importer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

// blockedImport builds an activeImport whose item channel has no consumer, so
// the first send from Import blocks. That is the state the shutdown deadlock
// needs: a caller inside Import, waiting for a receive that is never coming.
func blockedImport(t *testing.T) (*activeImport, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	return &activeImport{
		wg:              &sync.WaitGroup{},
		mutex:           &sync.RWMutex{},
		ctx:             ctx,
		stop:            cancel,
		itemChan:        make(chan Item),
		importedSources: make(map[string]struct{}),
	}, cancel
}

func testItem() Item {
	return Item{
		Source:   "test-source",
		InfoHash: protocol.ID{1},
		Name:     "A torrent",
	}
}

// A blocked send must not outlive the import's context. Before the fix Import
// blocked on an unbuffered channel unconditionally, so cancelling the context
// did nothing and the caller never returned.
func TestImportReturnsWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	ai, cancel := blockedImport(t)

	importErr := make(chan error, 1)
	go func() {
		importErr <- ai.Import(testItem())
	}()

	cancel()

	select {
	case err := <-importErr:
		require.ErrorIs(t, err, ErrImportClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("Import did not return after its context was cancelled")
	}
}

// The deadlock itself: the run loop calls Close when the context is cancelled,
// and Close takes the mutex that Import held across its blocking send. Neither
// goroutine could make progress, and http.Server.Shutdown waits for the request
// above them, so the process hung.
func TestCloseReturnsWhileAnImportIsWaitingToSend(t *testing.T) {
	t.Parallel()

	ai, _ := blockedImport(t)

	// Take exactly one item, then stop receiving. Once this fires, Import has
	// completed a send and is inside the next one - which is the moment the
	// unfixed code is holding the write lock.
	received := make(chan struct{})

	go func() {
		<-ai.itemChan
		close(received)
	}()

	importErr := make(chan error, 1)
	go func() {
		importErr <- ai.Import(testItem(), testItem())
	}()

	<-received

	closeErr := make(chan error, 1)
	go func() {
		closeErr <- ai.Close()
	}()

	select {
	case err := <-closeErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked while an import was waiting to send")
	}

	select {
	case err := <-importErr:
		require.ErrorIs(t, err, ErrImportClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("Import did not return after the import was closed")
	}
}

// Close is reached twice on the shutdown path - once by the run loop's
// cancellation branch and once by the HTTP handler - and closing an already
// closed import must stay harmless.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	ai, _ := blockedImport(t)

	require.NoError(t, ai.Close())
	require.True(t, ai.Closed())
	require.NoError(t, ai.Close())

	require.ErrorIs(t, ai.Import(testItem()), ErrImportClosed)
}

// The run loop is what a cancelled request or a shutdown actually reaches, and
// New is where its context is established. Cancelling the context the import was
// created with must end the loop and leave the import closed.
func TestRunLoopClosesTheImportWhenItsContextIsCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// A long wait time keeps the periodic flush out of this; nothing is
	// buffered, so no database is needed for the close.
	ai := importer{bufferSize: 100, maxWaitTime: time.Hour}.New(ctx, Info{ID: "test"})
	require.False(t, ai.Closed())

	cancel()

	require.Eventually(t, ai.Closed, 2*time.Second, time.Millisecond,
		"the run loop did not close the import after its context was cancelled")
	require.ErrorIs(t, ai.Import(testItem()), ErrImportClosed)
}
