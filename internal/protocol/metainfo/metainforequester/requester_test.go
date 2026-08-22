package metainforequester

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/peer_protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// umDataMessage builds an ut_metadata "data" message (msg_type 1) carrying the given
// piece index and payload, framed the way readMessage expects.
func umDataMessage(t *testing.T, piece int, payload []byte) []byte {
	t.Helper()

	dict, err := bencode.Marshal(extDict{MsgType: 1, Piece: piece})
	require.NoError(t, err)

	body := make([]byte, 0, 2+len(dict)+len(payload))
	body = append(body, byte(peer_protocol.Extended), 0x01)
	body = append(body, dict...)
	body = append(body, payload...)

	framed := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(framed, uint32(len(body)))

	return append(framed, body...)
}

func TestReadAllPieces_RejectsOutOfRangePieceIndex(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		piece int
	}{
		{name: "negative", piece: -1},
		{name: "past end", piece: 100000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg := umDataMessage(t, tc.piece, bytes.Repeat([]byte{'a'}, 16))

			_, err := readAllPieces(bytes.NewReader(msg), 16)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "piece index")
		})
	}
}

func TestReadAllPieces_RejectsDuplicatePiece(t *testing.T) {
	t.Parallel()

	// Two copies of piece 0, each a full 16kiB: the length accounting is satisfied
	// while the second half of the buffer is never written.
	full := bytes.Repeat([]byte{'a'}, 16*1024)

	var buf bytes.Buffer

	buf.Write(umDataMessage(t, 0, full))
	buf.Write(umDataMessage(t, 0, full))

	_, err := readAllPieces(&buf, 32*1024)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestReadAllPieces_AcceptsValidPieces(t *testing.T) {
	t.Parallel()

	first := bytes.Repeat([]byte{'a'}, 16*1024)
	second := bytes.Repeat([]byte{'b'}, 512)

	var buf bytes.Buffer

	buf.Write(umDataMessage(t, 0, first))
	buf.Write(umDataMessage(t, 1, second))

	got, err := readAllPieces(&buf, uint(len(first)+len(second)))

	require.NoError(t, err)
	assert.Equal(t, append(append([]byte{}, first...), second...), got)
}

// errWriter fails every write, so exHandshake's first write cannot succeed.
type errWriter struct {
	io.Reader
}

var errWriteFailed = errors.New("write failed")

func (errWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}

func TestExHandshake_PropagatesWriteError(t *testing.T) {
	t.Parallel()

	_, _, err := exHandshake(errWriter{Reader: strings.NewReader("")})

	require.ErrorIs(t, err, errWriteFailed)
}
