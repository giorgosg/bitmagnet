package server

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"
)

func TestQueryRespectsGlobalConcurrencyLimit(t *testing.T) {
	t.Parallel()

	socket := &queryTestSocket{sent: make(chan string, 2)}
	server := &server{
		socket:       socket,
		queryTimeout: time.Second,
		queries:      make(map[string]chan dht.RecvMsg),
		idIssuer:     &variantIDIssuer{},
		logger:       zap.NewNop().Sugar(),
		querySem:     semaphore.NewWeighted(1),
	}
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	errs := make(chan error, 2)

	go func() {
		_, err := server.Query(t.Context(), addr, dht.QPing, dht.MsgArgs{})
		errs <- err
	}()

	firstID := <-socket.sent

	go func() {
		_, err := server.Query(t.Context(), addr, dht.QPing, dht.MsgArgs{})
		errs <- err
	}()

	select {
	case secondID := <-socket.sent:
		t.Fatalf("second query sent before the first completed: %q", secondID)
	case <-time.After(100 * time.Millisecond):
	}

	server.handleResponse(successResponse(firstID))

	secondID := <-socket.sent
	server.handleResponse(successResponse(secondID))

	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
}

func TestQueryConcurrencyLimitUsesDefaultForNonPositiveConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value int64
		want  int64
	}{
		{name: "default", want: 512},
		{name: "zero", value: 0, want: 512},
		{name: "negative", value: -1, want: 512},
		{name: "configured", value: 7, want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			config := Config{MaxConcurrentQueries: tc.value}
			require.Equal(t, tc.want, config.queryConcurrencyLimit())
		})
	}
}

func successResponse(transactionID string) dht.RecvMsg {
	return dht.RecvMsg{
		Msg: dht.Msg{
			T: transactionID,
			Y: dht.YResponse,
			R: &dht.Return{},
		},
	}
}

type queryTestSocket struct {
	sent chan string
}

func (*queryTestSocket) Open(netip.AddrPort) error {
	return nil
}

func (*queryTestSocket) Close() error {
	return nil
}

func (s *queryTestSocket) Send(_ netip.AddrPort, data []byte) error {
	var msg dht.Msg
	if err := bencode.Unmarshal(data, &msg); err != nil {
		return err
	}

	s.sent <- msg.T

	return nil
}

func (*queryTestSocket) Receive([]byte) (int, netip.AddrPort, error) {
	return 0, netip.AddrPort{}, context.Canceled
}
