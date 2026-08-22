package dhtcrawler

import (
	"context"
	"net"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht/ktable"
)

func (c *crawler) reseedBootstrapNodes(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-c.reseedRequests:
		}

		for _, strAddr := range c.bootstrapNodes {
			addr, err := net.ResolveUDPAddr("udp", strAddr)
			if err != nil {
				c.logger.Warnf("failed to resolve bootstrap node address: %s", err)
				continue
			}

			select {
			case <-ctx.Done():
				return
			case c.nodesForPing.In() <- ktable.NewNode(ktable.ID{}, addr.AddrPort()):
				continue
			}
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		timer.Reset(c.reseedBootstrapNodesInterval)
	}
}

func (c *crawler) runKTableHealthMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	c.monitorKTableHealth(ctx, ticker.C, 3)
}

// monitorKTableHealth is adapted from o51r15/bitmagnet@9711ecbbb6c7d9644b99f78b92e4ade986dad24d.
// It checks actual table membership, not sample-infohash eligibility, before requesting a reseed.
func (c *crawler) monitorKTableHealth(ctx context.Context, ticks <-chan time.Time, dryThreshold int) {
	dryChecks := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			nodes := c.kTable.GetClosestNodes(ktable.ID{})
			if len(nodes) > 0 {
				dryChecks = 0
				continue
			}

			dryChecks++
			if dryChecks < dryThreshold {
				continue
			}

			c.requestBootstrapReseed()

			dryChecks = 0
		}
	}
}

func (c *crawler) requestBootstrapReseed() {
	select {
	case c.reseedRequests <- struct{}{}:
	default:
	}
}
