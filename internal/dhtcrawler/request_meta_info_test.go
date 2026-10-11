package dhtcrawler

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/blocking"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo/metainforequester"
	"github.com/stretchr/testify/require"
)

type metadataRequesterFunc func(context.Context, protocol.ID, netip.AddrPort) (metainforequester.Response, error)

func (f metadataRequesterFunc) Request(
	ctx context.Context,
	hash protocol.ID,
	peer netip.AddrPort,
) (metainforequester.Response, error) {
	return f(ctx, hash, peer)
}

type metadataCheckerFunc func(metainfo.Info) error

func (f metadataCheckerFunc) Check(info metainfo.Info) error { return f(info) }

type metadataBlockingManager struct {
	blocking.Manager
	block func(context.Context, []protocol.ID, bool) error
}

func (m metadataBlockingManager) Block(ctx context.Context, hashes []protocol.ID, flush bool) error {
	return m.block(ctx, hashes, flush)
}

func TestMetadataRequestReturnsFirstSuccessWithoutWaitingForStalledPeer(t *testing.T) {
	t.Parallel()

	stalledPeer := netip.MustParseAddrPort("127.0.0.1:10001")
	fastPeer := netip.MustParseAddrPort("127.0.0.1:10002")

	stalled := make(chan struct{})
	defer close(stalled)

	c := &crawler{
		metainfoRequester: metadataRequesterFunc(
			func(_ context.Context, _ protocol.ID, peer netip.AddrPort) (metainforequester.Response, error) {
				if peer == stalledPeer {
					<-stalled
				}

				return metainforequester.Response{Info: metainfo.Info{Name: peer.String()}}, nil
			},
		),
		banningChecker:       metadataCheckerFunc(func(metainfo.Info) error { return nil }),
		metadataRequestSlots: make(chan struct{}, 2),
	}

	done := make(chan metainforequester.Response, 1)

	go func() {
		response, _ := c.doRequestMetaInfo(t.Context(), protocol.ID{}, []netip.AddrPort{stalledPeer, fastPeer})
		done <- response
	}()

	select {
	case response := <-done:
		require.Equal(t, fastPeer.String(), response.Info.Name)
	case <-time.After(time.Second):
		t.Fatal("metadata request waited for the stalled peer despite a successful response")
	}
}

func TestMetadataRequestBoundsConcurrentPeersAcrossHashes(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 8)
	release := make(chan struct{})

	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	c := &crawler{
		metainfoRequester: metadataRequesterFunc(
			func(_ context.Context, _ protocol.ID, _ netip.AddrPort) (metainforequester.Response, error) {
				started <- struct{}{}

				<-release

				return metainforequester.Response{Info: metainfo.Info{Name: "ok"}}, nil
			},
		),
		banningChecker:       metadataCheckerFunc(func(metainfo.Info) error { return nil }),
		metadataRequestSlots: make(chan struct{}, 2),
	}
	peers := []netip.AddrPort{
		netip.MustParseAddrPort("127.0.0.1:10001"),
		netip.MustParseAddrPort("127.0.0.1:10002"),
		netip.MustParseAddrPort("127.0.0.1:10003"),
		netip.MustParseAddrPort("127.0.0.1:10004"),
	}
	done := make(chan error, 2)

	for range 2 {
		go func() {
			_, err := c.doRequestMetaInfo(t.Context(), protocol.ID{}, peers)
			done <- err
		}()
	}

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("metadata requests did not fill the shared concurrency limit")
		}
	}

	select {
	case <-started:
		t.Fatal("more than two peer requests ran across both hashes")
	case <-time.After(100 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })

	for range 2 {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("metadata request did not finish after peers were released")
		}
	}
}

func TestMetadataRequestBoundsConcurrentPeersPerHash(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 10)
	release := make(chan struct{})

	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	c := &crawler{
		metainfoRequester: metadataRequesterFunc(
			func(_ context.Context, _ protocol.ID, _ netip.AddrPort) (metainforequester.Response, error) {
				started <- struct{}{}

				<-release

				return metainforequester.Response{Info: metainfo.Info{Name: "ok"}}, nil
			},
		),
		banningChecker:       metadataCheckerFunc(func(metainfo.Info) error { return nil }),
		metadataRequestSlots: make(chan struct{}, 10),
	}

	peers := make([]netip.AddrPort, 10)
	for i := range peers {
		peers[i] = netip.MustParseAddrPort("127.0.0.1:10001")
	}

	done := make(chan error, 1)

	go func() {
		_, err := c.doRequestMetaInfo(t.Context(), protocol.ID{}, peers)
		done <- err
	}()

	for range maxParallelMetadataPeers {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("metadata requests did not fill the per-hash concurrency limit")
		}
	}

	select {
	case <-started:
		t.Fatal("more than five peer requests ran for one hash")
	case <-time.After(100 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("metadata request did not finish after peers were released")
	}
}

func TestMetadataRequestStopsOnCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 10)
	c := &crawler{
		metainfoRequester: metadataRequesterFunc(
			func(ctx context.Context, _ protocol.ID, _ netip.AddrPort) (metainforequester.Response, error) {
				started <- struct{}{}

				<-ctx.Done()

				return metainforequester.Response{}, ctx.Err()
			},
		),
		metadataRequestSlots: make(chan struct{}, 10),
	}

	peers := make([]netip.AddrPort, 100)
	for i := range peers {
		peers[i] = netip.MustParseAddrPort("127.0.0.1:10001")
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := c.doRequestMetaInfo(ctx, protocol.ID{}, peers)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata request did not start")
	}

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("metadata request did not stop on cancellation")
	}

	require.Eventually(t, func() bool { return len(c.metadataRequestSlots) == 0 }, time.Second, time.Millisecond)
}

func TestMetadataRequestJoinsAllPeerFailures(t *testing.T) {
	t.Parallel()

	firstErr, secondErr := errors.New("first failed"), errors.New("second failed")
	firstPeer := netip.MustParseAddrPort("127.0.0.1:10001")
	secondPeer := netip.MustParseAddrPort("127.0.0.1:10002")
	c := &crawler{
		metainfoRequester: metadataRequesterFunc(
			func(_ context.Context, _ protocol.ID, peer netip.AddrPort) (metainforequester.Response, error) {
				if peer == firstPeer {
					return metainforequester.Response{}, firstErr
				}

				return metainforequester.Response{}, secondErr
			},
		),
		metadataRequestSlots: make(chan struct{}, 2),
	}

	_, err := c.doRequestMetaInfo(t.Context(), protocol.ID{}, []netip.AddrPort{firstPeer, secondPeer})
	require.ErrorIs(t, err, firstErr)
	require.ErrorIs(t, err, secondErr)

	_, err = c.doRequestMetaInfo(t.Context(), protocol.ID{}, nil)
	require.ErrorContains(t, err, "no peers")
}

func TestMetadataRequestBlocksBannedMetadata(t *testing.T) {
	t.Parallel()

	banErr := errors.New("banned metadata")
	hash := protocol.RandomNodeID()
	blocked := make(chan []protocol.ID, 1)
	c := &crawler{
		metainfoRequester: metadataRequesterFunc(
			func(_ context.Context, _ protocol.ID, _ netip.AddrPort) (metainforequester.Response, error) {
				return metainforequester.Response{Info: metainfo.Info{Name: "banned"}}, nil
			},
		),
		banningChecker: metadataCheckerFunc(func(info metainfo.Info) error {
			if info.Name == "banned" {
				return banErr
			}

			return nil
		}),
		blockingManager: metadataBlockingManager{
			block: func(_ context.Context, hashes []protocol.ID, flush bool) error {
				require.False(t, flush)

				blocked <- hashes

				return nil
			},
		},
		metadataRequestSlots: make(chan struct{}, 1),
	}

	response, err := c.doRequestMetaInfo(
		t.Context(),
		hash,
		[]netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:10001")},
	)
	require.ErrorIs(t, err, banErr)
	require.Empty(t, response.Info.Name)
	require.Equal(t, []protocol.ID{hash}, <-blocked)
}
