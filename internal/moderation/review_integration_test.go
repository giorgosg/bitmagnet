package moderation_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func normalClassificationJob(hash protocol.ID) (model.QueueJob, error) {
	return processor.NewQueueJob(processor.MessageParams{InfoHashes: []protocol.ID{hash}})
}

func TestReviewRollsBackAllowWhenClassificationCannotBeQueued(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	_, err := db.Pool.Exec(t.Context(), `DELETE FROM torrent_contents WHERE info_hash=$1`, hashes[1][:])
	require.NoError(t, err)

	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(hashes[1])
	before, err := service.Get(t.Context(), id)
	require.NoError(t, err)

	queueFailure := errors.New("synthetic queue construction failure")
	_, err = service.Review(
		t.Context(),
		id,
		"allow",
		before.UpdatedAt,
		"reviewer",
		nil,
		func(protocol.ID) (model.QueueJob, error) { return model.QueueJob{}, queueFailure },
	)
	require.ErrorIs(t, err, queueFailure)
	after, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Pending, after.Status)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
	allowed, err := service.Review(
		t.Context(),
		id,
		"allow",
		before.UpdatedAt,
		"reviewer",
		nil,
		normalClassificationJob,
	)
	require.NoError(t, err)
	require.True(t, allowed.ClassificationQueued)
	require.NoError(
		t,
		service.ApplyAutomatic(
			t.Context(),
			id,
			moderation.Banned,
			moderation.Evidence{Rule: "changed synthetic rule"},
		),
	)
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Moderator, "synthetic later import"))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Allowed, record.Status)
}

func TestReviewAllowsOnlyOneConcurrentDecisionAtObservedVersion(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{81})
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Automatic, "synthetic"))
	before, err := service.Get(t.Context(), id)
	require.NoError(t, err)

	var wg sync.WaitGroup

	results := make(chan error, 2)
	start := make(chan struct{})

	for _, action := range []string{"allow", "ban"} {
		wg.Go(func() {
			<-start

			_, err := service.Review(
				t.Context(),
				id,
				action,
				before.UpdatedAt,
				"reviewer",
				nil,
				normalClassificationJob,
			)
			results <- err
		})
	}

	close(start)
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0

	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, moderation.ErrConflict)

			conflicts++
		}
	}

	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
}
