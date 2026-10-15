package processor

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type EnhancementWorkerParams struct {
	fx.In
	Config    enhancement.Config
	Dao       lazy.Lazy[*dao.Query]
	Processor lazy.Lazy[Processor]
	Catalogue lazy.Lazy[tmdb.Client]
	Logger    *zap.SugaredLogger
}
type EnhancementWorkerResult struct {
	fx.Out
	Worker worker.Worker `group:"workers"`
}

func NewEnhancementWorker(p EnhancementWorkerParams) EnhancementWorkerResult {
	var (
		cancel context.CancelFunc
		wg     sync.WaitGroup
	)

	return EnhancementWorkerResult{Worker: worker.NewWorker("enhancement", fx.Hook{
		OnStart: func(start context.Context) error {
			if !p.Config.Automatic {
				return nil
			}

			if err := p.Config.Validate(); err != nil {
				return err
			}

			d, err := p.Dao.Get()
			if err != nil {
				return err
			}

			proc, err := p.Processor.Get()
			if err != nil {
				return err
			}

			cat, err := p.Catalogue.Get()
			if err != nil {
				return err
			}

			ctx, stop := context.WithCancel(context.WithoutCancel(start))
			cancel = stop

			for range p.Config.Concurrency {
				wg.Add(1)
				go func() {
					defer wg.Done()

					scheduler := enhancement.NewScheduler(
						d.UnderlyingDB(),
						p.Config,
						nil,
						func(ctx context.Context, h protocol.ID) error {
							return proc.Process(
								ctx,
								MessageParams{InfoHashes: []protocol.ID{h}},
							)
						},
					)
					for ctx.Err() == nil {
						claim, err := scheduler.Claim(ctx)
						if err == nil && claim.Token != "" {
							// Run counters are process-local in read-only evaluation;
							// automatic runs get fresh bounded evaluators plus shared
							// request accounting and leases.
							engine, newErr := enhancement.NewEvaluator(
								p.Config,
								p.Catalogue,
								http.DefaultClient,
							)
							if newErr != nil {
								p.Logger.Errorw(
									"enhancement configuration failure",
									"error",
									newErr,
								)

								return
							}

							app := enhancement.NewApplication(
								d.UnderlyingDB(),
								engine,
								cat,
								p.Config,
							)
							owned := enhancement.NewScheduler(
								d.UnderlyingDB(),
								p.Config,
								app,
								func(ctx context.Context, h protocol.ID) error {
									return proc.Process(
										ctx,
										MessageParams{
											InfoHashes: []protocol.ID{h},
										},
									)
								},
							)

							_, err = owned.Process(ctx, claim)
							if err == nil || errors.Is(err, enhancement.ErrEvaluated) {
								continue
							}
						}

						if err != nil && !errors.Is(err, enhancement.ErrPaused) &&
							ctx.Err() == nil {
							p.Logger.Warnw("enhancement processing", "error", err)
						}

						timer := time.NewTimer(p.Config.PollInterval)
						select {
						case <-ctx.Done():
							timer.Stop()
							return
						case <-timer.C:
						}
					}
				}()
			}

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel == nil {
				return nil
			}

			cancel()

			done := make(chan struct{})

			go func() { wg.Wait(); close(done) }()

			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})}
}
