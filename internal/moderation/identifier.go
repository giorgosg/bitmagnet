// Package moderation provides canonical identifiers for harmful-content moderation.
package moderation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
)

// Identifier is the SHA-256 membership digest of a canonical BitTorrent v1 infohash.
type Identifier [32]byte

// IdentifierFormatV1 names the SHA-256 digest of the contextualized raw BTIH bytes.
const IdentifierFormatV1 = "sha256-btih-v1"

// IdentifierForInfoHash computes the version-1 identifier from raw 20-byte infohash bytes.
func IdentifierForInfoHash(infoHash protocol.ID) Identifier {
	preimage := append([]byte("magnes:moderation:v1:btih:"), infoHash[:]...)
	return sha256.Sum256(preimage)
}

// String returns the canonical lowercase hexadecimal digest.
func (id Identifier) String() string {
	return hex.EncodeToString(id[:])
}

// ParseIdentifier accepts a versioned canonical hexadecimal digest.
func ParseIdentifier(format, digest string) (Identifier, error) {
	if format != IdentifierFormatV1 {
		return Identifier{}, errors.New("unsupported moderation identifier format")
	}

	if len(digest) != 64 {
		return Identifier{}, errors.New(
			"moderation identifier must contain 64 lowercase hexadecimal characters",
		)
	}

	decoded, err := hex.DecodeString(digest)
	if err != nil {
		return Identifier{}, fmt.Errorf("invalid moderation identifier: %w", err)
	}

	var id Identifier
	copy(id[:], decoded)

	if id.String() != digest {
		return Identifier{}, errors.New("moderation identifier must be lowercase hexadecimal")
	}

	return id, nil
}
