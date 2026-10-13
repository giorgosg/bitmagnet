package importer

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/classifier"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestInitialImportKeywordDetectionRetainsOnlyObservedMetadata(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{123}
	ledger := moderation.NewService(db.Gorm)
	cfg := classifier.NewDefaultConfig()
	cfg.Keywords = map[string][]string{"banned": {"synthetic review marker"}}
	cr := classifier.New(classifier.Params{Config: cfg})
	factory := New(
		Params{
			Dao:           lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
			KeywordPolicy: cr.KeywordPolicy,
		},
	)
	importer, err := factory.Importer.Get()
	require.NoError(t, err)
	imp := importer.New(t.Context(), Info{})
	require.NoError(
		t,
		imp.Import(Item{Source: "synthetic", InfoHash: hash, Name: "Harmless synthetic review marker"}),
	)
	imp.Drain()
	require.NoError(t, imp.Close())
	record, err := ledger.Get(t.Context(), moderation.IdentifierForInfoHash(hash))
	require.NoError(t, err)
	require.Equal(t, moderation.Pending, record.Status)
	require.Equal(t, "Harmless synthetic review marker", record.Evidence.Name)
	require.Nil(t, record.Evidence.Files, "imports without metainfo must not invent a filename")
}
