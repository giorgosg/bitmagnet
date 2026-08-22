package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/processor/batch"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/queue/handler"
	"go.uber.org/fx"
	"gorm.io/gen"
)

type Params struct {
	fx.In
	Dao lazy.Lazy[*dao.Query]
}

type Result struct {
	fx.Out
	Handler lazy.Lazy[handler.Handler] `group:"queue_handlers"`
}

func New(p Params) Result {
	return Result{
		Handler: lazy.New(func() (handler.Handler, error) {
			d, err := p.Dao.Get()
			if err != nil {
				return handler.Handler{}, err
			}

			return handler.New(
				batch.MessageName,
				func(ctx context.Context, job model.QueueJob) (err error) {
					msg := &batch.MessageParams{}
					if err := json.Unmarshal([]byte(job.Payload), msg); err != nil {
						return err
					}

					var scopes []func(gen.Dao) gen.Dao
					if len(msg.ContentTypes) > 0 {
						scopes = append(scopes, contentTypeScope(d, msg.ContentTypes))
					}

					if msg.Orphans {
						scopes = append(scopes, func(tx gen.Dao) gen.Dao {
							return tx.Not(
								gen.Exists(
									d.TorrentContent.Where(
										d.TorrentContent.InfoHash.EqCol(
											d.Torrent.InfoHash,
										),
									),
								),
							)
						})
					}

					priority := 10
					// prioritise jobs where API calls are disabled as they will run faster:
					if msg.ApisDisabled() {
						priority = 4
					}

					maxInfoHash := msg.InfoHashGreaterThan
					chunkSize := uint(0)
					done := false

					var queueJobs []*model.QueueJob

					for {
						torrents, findErr := d.Torrent.WithContext(ctx).
							Scopes(scopes...).
							Where(
								d.Torrent.InfoHash.Gt(maxInfoHash),
								d.Torrent.UpdatedAt.Lt(msg.UpdatedBefore),
							).
							Select(d.Torrent.InfoHash).
							Order(d.Torrent.InfoHash).
							Limit(int(msg.BatchSize)).
							Find()
						if findErr != nil {
							return findErr
						}

						if len(torrents) == 0 {
							done = true
							break
						}

						var infoHashes []protocol.ID

						for _, t := range torrents {
							maxInfoHash = t.InfoHash
							infoHashes = append(infoHashes, t.InfoHash)
							chunkSize++
						}

						job, jobErr := processor.NewQueueJob(processor.MessageParams{
							ClassifyMode:       msg.ClassifyMode,
							ClassifierWorkflow: msg.ClassifierWorkflow,
							ClassifierFlags:    msg.ClassifierFlags,
							InfoHashes:         infoHashes,
						}, model.QueueJobPriority(priority))
						if jobErr != nil {
							return jobErr
						}

						queueJobs = append(queueJobs, &job)

						if len(torrents) < int(msg.BatchSize) {
							done = true
							break
						}

						if chunkSize >= msg.ChunkSize {
							break
						}
					}

					if !done {
						job, jobErr := batch.NewQueueJob(batch.MessageParams{
							InfoHashGreaterThan: maxInfoHash,
							UpdatedBefore:       msg.UpdatedBefore,
							ClassifyMode:        msg.ClassifyMode,
							ClassifierWorkflow:  msg.ClassifierWorkflow,
							ClassifierFlags:     msg.ClassifierFlags,
							ChunkSize:           msg.ChunkSize,
							BatchSize:           msg.BatchSize,
							ContentTypes:        msg.ContentTypes,
							Orphans:             msg.Orphans,
						})
						if jobErr != nil {
							return jobErr
						}

						queueJobs = append(queueJobs, &job)
					}

					if len(queueJobs) > 0 {
						if createErr := d.QueueJob.
							WithContext(ctx).
							Create(queueJobs...); createErr != nil {
							return createErr
						}
					}

					return nil
				},
				handler.JobTimeout(time.Second*60*10),
				handler.Concurrency(1),
			), nil
		}),
	}
}

// contentTypeScope restricts a torrent query to content rows matching one of
// the requested types. An invalid NullContentType means "unclassified".
//
// Adapted from kawaii-not-kawaii/bitmagnet@f5f027a45. The predicate must be
// grouped with the info-hash correlation: applying Or to the whole subquery
// makes an unclassified content row satisfy EXISTS for every torrent.
func contentTypeScope(d *dao.Query, nullable []model.NullContentType) func(gen.Dao) gen.Dao {
	var (
		contentTypes []string
		unknown      bool
	)

	for _, contentType := range nullable {
		if contentType.Valid {
			contentTypes = append(contentTypes, contentType.ContentType.String())
		} else {
			unknown = true
		}
	}

	return func(tx gen.Dao) gen.Dao {
		var condition gen.Condition

		switch {
		case len(contentTypes) > 0 && unknown:
			condition = d.TorrentContent.Where(
				d.TorrentContent.ContentType.In(contentTypes...),
			).Or(d.TorrentContent.ContentType.IsNull())
		case unknown:
			condition = d.TorrentContent.ContentType.IsNull()
		default:
			condition = d.TorrentContent.ContentType.In(contentTypes...)
		}

		return tx.Where(gen.Exists(d.TorrentContent.Where(
			d.TorrentContent.InfoHash.EqCol(d.Torrent.InfoHash),
			condition,
		)))
	}
}
