package moderation_test

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestIdentifierForInfoHashCanonicalVectors(t *testing.T) {
	t.Parallel()

	// Independently calculated with Python hashlib; see ADR 0006 for the preimages.
	for _, test := range []struct {
		name string
		hash string
		want string
	}{
		{
			name: "zero",
			hash: "0000000000000000000000000000000000000000",
			want: "20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6",
		},
		{
			name: "all high bits",
			hash: "ffffffffffffffffffffffffffffffffffffffff",
			want: "9afaa45890d2acf0f8760eb8c7bcb8f55193d8eadf816f1165a02410b68912be",
		},
		{
			name: "ascending",
			hash: "000102030405060708090a0b0c0d0e0f10111213",
			want: "41a75932cf9e555dca1575a5029f6a665d8ec8ef568e978298a9dbff8c9a913b",
		},
		{
			name: "mixed high bits",
			hash: "0080ff017f0281fe030405060708090a0b0c0d0e",
			want: "25e64157619734bf1d1fc41154d6cf1caecc7cab0ca5446f7b308c34dd2f1091",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(
				t,
				test.want,
				moderation.IdentifierForInfoHash(protocol.MustParseID(test.hash)).String(),
			)
		})
	}
}

func TestParseIdentifierPreservesCanonicalDigest(t *testing.T) {
	t.Parallel()

	const digest = "41a75932cf9e555dca1575a5029f6a665d8ec8ef568e978298a9dbff8c9a913b"

	id, err := moderation.ParseIdentifier(moderation.IdentifierFormatV1, digest)
	if assert.NoError(t, err) {
		assert.Equal(t, digest, id.String())
	}
}

func TestParseIdentifierRejectsNonCanonicalFormats(t *testing.T) {
	t.Parallel()

	const digest = "41a75932cf9e555dca1575a5029f6a665d8ec8ef568e978298a9dbff8c9a913b"
	for _, test := range []struct {
		name   string
		format string
		digest string
	}{
		{"missing format", "", digest},
		{"future version", "sha256-btih-v2", digest},
		{"different hash kind", "sha256-btmh-v1", digest},
		{"format case", "SHA256-BTIH-V1", digest},
		{"empty digest", moderation.IdentifierFormatV1, ""},
		{"short digest", moderation.IdentifierFormatV1, digest[:62]},
		{"long digest", moderation.IdentifierFormatV1, digest + "00"},
		{"invalid hex", moderation.IdentifierFormatV1, "z" + digest[1:]},
		{"uppercase hex", moderation.IdentifierFormatV1, "41A" + digest[3:]},
		{"hex prefix", moderation.IdentifierFormatV1, "0x" + digest},
		{"surrounding whitespace", moderation.IdentifierFormatV1, " " + digest + " "},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := moderation.ParseIdentifier(test.format, test.digest)
			assert.Error(t, err)
		})
	}
}

func TestEquivalentMagnetsHaveOneModerationIdentifier(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{
		"magnet:?xt=urn:btih:000102030405060708090a0b0c0d0e0f10111213",
		"magnet:?dn=Harmless+fixture&xt=urn:btih:000102030405060708090A0B0C0D0E0F10111213" +
			"&tr=https%3A%2F%2Fexample.invalid%2Fannounce",
		"magnet:?xt=urn:btih:AAAQEAYEAUDAOCAJBIFQYDIOB4IBCEQT&dn=Another+name",
	} {
		t.Run(uri, func(t *testing.T) {
			t.Parallel()

			magnet, err := metainfo.ParseMagnetUri(uri)
			if assert.NoError(t, err) {
				assert.Equal(t, "41a75932cf9e555dca1575a5029f6a665d8ec8ef568e978298a9dbff8c9a913b",
					moderation.IdentifierForInfoHash(protocol.ID(magnet.InfoHash)).String())
			}
		})
	}
}
