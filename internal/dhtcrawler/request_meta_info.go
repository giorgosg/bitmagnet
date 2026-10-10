package dhtcrawler

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo/metainforequester"
)

const maxParallelMetadataPeers = 5

type metadataRequestResult struct {
	peer     netip.AddrPort
	response metainforequester.Response
	err      error
}

func (c *crawler) runRequestMetaInfo(ctx context.Context) {
	_ = c.requestMetaInfo.Run(ctx, func(req infoHashWithPeers) {
		mi, reqErr := c.doRequestMetaInfo(ctx, req.infoHash, req.peers)
		if reqErr != nil {
			return
		}

		select {
		case <-ctx.Done():
		case c.persistTorrents.In() <- infoHashWithMetaInfo{
			nodeHasPeersForHash: req.nodeHasPeersForHash,
			metaInfo:            mi.Info,
		}:
		}
	})
}

func (c *crawler) doRequestMetaInfo(
	ctx context.Context,
	hash protocol.ID,
	peers []netip.AddrPort,
) (metainforequester.Response, error) {
	if len(peers) == 0 {
		return metainforequester.Response{}, errors.New("no peers available for metadata request")
	}

	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan metadataRequestResult, maxParallelMetadataPeers)

	var errs []error

	nextPeer, inFlight := 0, 0

	for nextPeer < len(peers) || inFlight > 0 {
		if err := ctx.Err(); err != nil {
			return metainforequester.Response{}, err
		}

		var slots chan struct{}
		if nextPeer < len(peers) && inFlight < maxParallelMetadataPeers {
			slots = c.metadataRequestSlots
		}

		select {
		case <-ctx.Done():
			return metainforequester.Response{}, ctx.Err()
		case slots <- struct{}{}:
			peer := peers[nextPeer]
			nextPeer++
			inFlight++

			go func() {
				defer func() { <-c.metadataRequestSlots }()

				response, err := c.metainfoRequester.Request(requestCtx, hash, peer)
				select {
				case results <- metadataRequestResult{peer: peer, response: response, err: err}:
				case <-requestCtx.Done():
				}
			}()
		case result := <-results:
			inFlight--

			if result.err != nil {
				errs = append(errs, fmt.Errorf("peer %s: %w", result.peer, result.err))
				continue
			}

			if banErr := c.banningChecker.Check(result.response.Info); banErr != nil {
				cancel()

				_ = c.blockingManager.Block(ctx, []protocol.ID{hash}, false)

				return metainforequester.Response{}, banErr
			}

			return result.response, nil
		}
	}

	if err := ctx.Err(); err != nil {
		return metainforequester.Response{}, err
	}

	return metainforequester.Response{}, errors.Join(errs...)
}
