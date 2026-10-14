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
	"gorm.io/gorm"
)

func normalClassificationJob(hash protocol.ID) (model.QueueJob, error) {
	return processor.NewQueueJob(processor.MessageParams{InfoHashes: []protocol.ID{hash}})
}

func TestReviewRejectsChangedPrivateEvidenceAtObservedRevision(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"retain", "ingest", "purge"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			db := dbtest.New(t)
			service := moderation.NewService(db.Gorm)
			hash := protocol.ID{217, 48}
			id := moderation.IdentifierForInfoHash(hash)
			require.NoError(t, service.Flag(t.Context(), id, moderation.Evidence{
				InfoHash: &hash, Name: "Synthetic review", Files: []string{"first/harmless.txt"},
			}, "fixture"))
			observed, err := service.Get(t.Context(), id)
			require.NoError(t, err)

			evidence := moderation.Evidence{InfoHash: &hash, Files: []string{"changed/harmless.txt"}}

			switch path {
			case "retain":
				require.NoError(t, service.RetainEvidence(t.Context(), id, evidence))
			case "ingest":
				require.NoError(t, service.Ingest(t.Context(), []moderation.Evidence{evidence},
					func(*gorm.DB, map[protocol.ID]bool) error { return nil }))
			case "purge":
				require.NoError(t, service.PurgeEvidence(t.Context(), id))
			}

			_, err = service.Review(t.Context(), id, moderation.ReviewAllow, observed.UpdatedAt,
				"reviewer", nil, normalClassificationJob)
			require.ErrorIs(t, err, moderation.ErrConflict,
				"changed private evidence must invalidate the observed review revision")
			current, err := service.Get(t.Context(), id)
			require.NoError(t, err)
			require.Equal(t, moderation.Pending, current.Status)
			require.True(t, current.UpdatedAt.After(observed.UpdatedAt))

			if path == "purge" {
				require.Nil(t, current.Evidence)
			} else {
				require.Equal(t, []string{"changed/harmless.txt"}, current.Evidence.Files)
				require.NoError(t, service.RetainEvidence(t.Context(), id, evidence))
				unchanged, err := service.Get(t.Context(), id)
				require.NoError(t, err)
				require.Equal(t, current.UpdatedAt, unchanged.UpdatedAt)
			}

			_, err = service.Review(t.Context(), id, moderation.ReviewAllow, current.UpdatedAt,
				"reviewer", nil, normalClassificationJob)
			require.NoError(t, err)
		})
	}
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
		moderation.ReviewAllow,
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
		moderation.ReviewAllow,
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

	for _, action := range []moderation.ReviewAction{moderation.ReviewAllow, moderation.ReviewBan} {
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

func TestReviewRevisionAdvancesWhenClockPredatesObservedVersion(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{218, 48})
	require.NoError(
		t,
		service.Flag(t.Context(), id, moderation.Evidence{Name: "Synthetic clock fixture"}, "fixture"),
	)
	// A future fixture timestamp simulates an earlier writer's clock being ahead.
	_, err := db.Pool.Exec(
		t.Context(),
		`UPDATE moderation_decisions SET updated_at = '2100-01-01T00:00:00.123456Z' WHERE identifier = $1`,
		id.String(),
	)
	require.NoError(t, err)
	observed, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	allowed, err := service.Review(t.Context(), id, moderation.ReviewAllow, observed.UpdatedAt,
		"reviewer", nil, normalClassificationJob)
	require.NoError(t, err)
	require.True(
		t,
		allowed.Record.UpdatedAt.After(observed.UpdatedAt),
		"review revisions must advance even when clocks disagree",
	)
	require.NoError(t, service.Allow(t.Context(), id, "reviewer"))
	unchanged, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, allowed.Record.UpdatedAt, unchanged.UpdatedAt, "an idempotent decision preserves its revision")
}
