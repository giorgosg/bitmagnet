package responder

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht/ktable"
)

type Responder interface {
	Respond(context.Context, dht.RecvMsg) (dht.Return, error)
}

type responder struct {
	nodeID                   protocol.ID
	kTable                   ktable.Table
	tokenSecret              []byte
	sampleInfoHashesInterval int64
}

var ErrMissingArguments = dht.Error{
	Code: dht.ErrorCodeProtocolError,
	Msg:  "missing arguments",
}

// tokenLength is how many bytes of the announce token HMAC are kept. Sixteen
// bytes gives the same 32 hex characters the previous md5-based token produced,
// so the wire format is unchanged.
const tokenLength = 16

var ErrInvalidToken = dht.Error{
	Code: dht.ErrorCodeProtocolError,
	Msg:  "invalid token",
}

var ErrMethodUnknown = dht.Error{
	Code: dht.ErrorCodeMethodUnknown,
	Msg:  "method Unknown",
}

var ErrTooManyRequests = dht.Error{
	Code: dht.ErrorCodeGenericError,
	Msg:  "too many requests",
}

func (r responder) Respond(_ context.Context, msg dht.RecvMsg) (ret dht.Return, err error) {
	args := msg.Msg.A
	if args == nil {
		err = ErrMissingArguments
		return
	}

	ret.ID = r.nodeID

	switch msg.Msg.Q {
	case dht.QPing:
	case dht.QFindNode:
		if args.Target == [20]byte{} {
			err = ErrMissingArguments
			return
		}

		closestNodes := r.kTable.GetClosestNodes(args.Target)
		ret.Nodes = nodeInfosFromNodes(closestNodes...)
	case dht.QGetPeers:
		if args.InfoHash == [20]byte{} {
			err = ErrMissingArguments
			return
		}

		result := r.kTable.GetHashOrClosestNodes(args.InfoHash)
		if result.Found {
			hashPeers := result.Hash.Peers()
			values := make([]dht.NodeAddr, 0, len(hashPeers))

			for _, p := range hashPeers {
				values = append(values, dht.NewNodeAddrFromAddrPort(p.Addr))
			}

			ret.Values = values
		}

		ret.Nodes = nodeInfosFromNodes(result.ClosestNodes...)
		token := r.announceToken(args.InfoHash, args.ID, msg.From.Addr())
		ret.Token = &token
	case dht.QAnnouncePeer:
		if args.InfoHash == [20]byte{} {
			err = ErrMissingArguments
			return
		}

		if !r.validAnnounceToken(args.Token, args.InfoHash, args.ID, msg.From.Addr()) {
			err = ErrInvalidToken
			return
		}

		r.kTable.BatchCommand(ktable.PutHash{ID: args.InfoHash, Peers: []ktable.HashPeer{{
			Addr: netip.AddrPortFrom(msg.From.Addr(), msg.AnnouncePort()),
		}}})
	case dht.QSampleInfohashes:
		result := r.kTable.SampleHashesAndNodes()
		samples := make(dht.CompactInfohashes, 0, len(result.Hashes))

		for _, h := range result.Hashes {
			samples = append(samples, h.ID())
		}

		ret.Samples = &samples
		ret.Nodes = nodeInfosFromNodes(result.Nodes...)
		numInt64 := int64(result.TotalHashes)
		ret.Num = &numInt64
		ret.Interval = &r.sampleInfoHashesInterval
	default:
		err = ErrMethodUnknown
		return
	}

	return
}

// announceToken returns the token for an announce_peer request.
// A "token" key is included in the get_peers return value.
// The token value is a required argument for a future announce_peer query.
// The token value should be a short binary string.
// The queried node must verify that the token was previously sent to the same IP address as the querying node.
// Then the queried node should store the IP address of the querying node and the supplied port number
// under the infohash in its store of peer contact information.
// https://www.bittorrent.org/beps/bep_0005.html
//
// The token is a MAC over the querying node's identity and address, keyed by a
// per-process secret. It used to be md5(secret || message), which is the textbook
// length-extension-vulnerable construction: MD5 is Merkle-Damgard, so knowing one
// valid token lets an attacker compute a token for that message plus a suffix,
// without knowing the secret. The address is the final and only variable-length
// field, which is exactly the suffix position. HMAC is not vulnerable to this.
//
// The digest is truncated to tokenLength bytes so the token stays the same size on
// the wire as before, BEP 5 asking for "a short binary string". Truncating an HMAC
// is sound.
func (r responder) announceToken(infoHash protocol.ID, nodeID protocol.ID, nodeAddr netip.Addr) string {
	mac := hmac.New(sha256.New, r.tokenSecret)
	// hash.Hash never returns an error from Write.
	mac.Write(r.nodeID[:])
	mac.Write(infoHash[:])
	mac.Write(nodeID[:])
	mac.Write([]byte(nodeAddr.String()))

	return hex.EncodeToString(mac.Sum(nil)[:tokenLength])
}

// validAnnounceToken reports whether token is the one this node issued for these
// arguments, comparing in constant time so a wrong token cannot be recovered a byte
// at a time by measuring how long the rejection takes.
func (r responder) validAnnounceToken(
	token string,
	infoHash protocol.ID,
	nodeID protocol.ID,
	nodeAddr netip.Addr,
) bool {
	expected := r.announceToken(infoHash, nodeID, nodeAddr)

	return hmac.Equal([]byte(token), []byte(expected))
}

func nodeInfosFromNodes(ns ...ktable.Node) []dht.NodeInfo {
	if len(ns) == 0 {
		return nil
	}

	nodes := make([]dht.NodeInfo, 0, len(ns))
	for _, n := range ns {
		nodes = append(nodes, nodeInfoFromNode(n))
	}

	return nodes
}

func nodeInfoFromNode(n ktable.Node) dht.NodeInfo {
	return dht.NodeInfo{
		ID:   n.ID(),
		Addr: dht.NewNodeAddrFromAddrPort(n.Addr()),
	}
}
