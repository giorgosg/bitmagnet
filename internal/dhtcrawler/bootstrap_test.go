package dhtcrawler

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht/ktable"
	"github.com/stretchr/testify/require"
)

func TestKTableHealthMonitorDoesNotReseedPopulatedTable(t *testing.T) {
	t.Parallel()

	table := ktable.New(ktable.Params{NodeID: protocol.RandomNodeID()}).Table
	table.PutNode(
		protocol.RandomNodeID(),
		netip.MustParseAddrPort("127.0.0.1:6881"),
		ktable.NodeBep51Support(false),
	)

	c := crawler{
		kTable:         table,
		reseedRequests: make(chan struct{}, 1),
	}
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		c.monitorKTableHealth(ctx, ticks, 3)
	}()

	for range 3 {
		ticks <- time.Now()
	}

	select {
	case <-c.reseedRequests:
		t.Fatal("requested a reseed for a populated routing table")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health monitor did not stop after cancellation")
	}
}

func TestKTableHealthMonitorRequestsReseedAfterConsecutiveEmptyChecks(t *testing.T) {
	t.Parallel()

	table := ktable.New(ktable.Params{NodeID: protocol.RandomNodeID()}).Table
	c := crawler{
		kTable:         table,
		reseedRequests: make(chan struct{}, 1),
	}
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(t.Context())

	defer cancel()

	go c.monitorKTableHealth(ctx, ticks, 3)

	for range 2 {
		ticks <- time.Now()
	}

	select {
	case <-c.reseedRequests:
		t.Fatal("requested a reseed before the empty-table threshold")
	default:
	}

	ticks <- time.Now()

	select {
	case <-c.reseedRequests:
	case <-time.After(time.Second):
		t.Fatal("did not request a reseed after the empty-table threshold")
	}

	require.Empty(t, c.reseedRequests)
}
