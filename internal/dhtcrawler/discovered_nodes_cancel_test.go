package dhtcrawler

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht/client"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht/ktable"
	"github.com/stretchr/testify/require"
)

type cancelledDiscoveryChannel struct {
	inCalls atomic.Int32
	in      chan ktable.Node
}

func (c *cancelledDiscoveryChannel) In() chan<- ktable.Node {
	c.inCalls.Add(1)
	return c.in
}
func (*cancelledDiscoveryChannel) Out() <-chan []ktable.Node { return nil }
func (*cancelledDiscoveryChannel) Close()                    {}

type cancelledDiscoveryClient struct {
	client.Client
	nodes []client.NodeInfo
}

func (c cancelledDiscoveryClient) GetPeers(
	context.Context,
	netip.AddrPort,
	protocol.ID,
) (client.GetPeersResult, error) {
	return client.GetPeersResult{
		Nodes:  c.nodes,
		Values: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:10002")},
	}, nil
}

func (c cancelledDiscoveryClient) GetPeersScrape(
	context.Context,
	netip.AddrPort,
	protocol.ID,
) (client.GetPeersScrapeResult, error) {
	return client.GetPeersScrapeResult{Nodes: c.nodes}, nil
}

func TestDiscoveryStopsWalkingNodesAfterCancellation(t *testing.T) {
	t.Parallel()

	for name, request := range map[string]func(*crawler, context.Context, nodeHasPeersForHash) error{
		"get peers": func(c *crawler, ctx context.Context, req nodeHasPeersForHash) error {
			_, err := c.requestPeersForHash(ctx, req)
			return err
		},
		"scrape": func(c *crawler, ctx context.Context, req nodeHasPeersForHash) error {
			_, err := c.requestScrape(ctx, req)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			discovered := &cancelledDiscoveryChannel{in: make(chan ktable.Node)}
			c := &crawler{
				client: cancelledDiscoveryClient{nodes: []client.NodeInfo{
					{Addr: netip.MustParseAddrPort("127.0.0.1:10003")},
					{Addr: netip.MustParseAddrPort("127.0.0.1:10004")},
					{Addr: netip.MustParseAddrPort("127.0.0.1:10005")},
				}},
				kTable:          ktable.New(ktable.Params{NodeID: protocol.RandomNodeID()}).Table,
				discoveredNodes: discovered,
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := request(c, ctx, nodeHasPeersForHash{node: netip.MustParseAddrPort("127.0.0.1:10001")})
			require.NoError(t, err)
			require.EqualValues(
				t,
				1,
				discovered.inCalls.Load(),
				"discovery should stop when its context is cancelled",
			)
		})
	}
}
