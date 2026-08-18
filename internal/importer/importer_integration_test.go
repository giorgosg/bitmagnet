package importer

import (
	"context"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestPersistItemsSkipsExistingQueueJob(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	importer := activeImport{
		importer: importer{
			dao: db.Query,
		},
		ctx:             context.Background(),
		importedSources: make(map[string]struct{}),
	}
	item := Item{
		Source:   "test-source",
		InfoHash: protocol.ID{1},
		Name:     "A torrent",
	}

	require.NoError(t, importer.persistItems(item))
	require.NoError(t, importer.persistItems(item))
}
