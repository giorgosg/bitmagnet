package search

import (
	"context"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"gorm.io/gen/field"
	"gorm.io/gorm/clause"
)

type TorrentContentResultItem struct {
	query.ResultItem
	model.TorrentContent
}

type TorrentContentResult = query.GenericResult[TorrentContentResultItem]

type TorrentContentSearch interface {
	TorrentContent(ctx context.Context, options ...query.Option) (TorrentContentResult, error)
}

func (s search) TorrentContent(ctx context.Context, options ...query.Option) (TorrentContentResult, error) {
	return query.GenericQuery[TorrentContentResultItem](
		ctx,
		s.q,
		catalogueOptions(options),
		model.TableNameTorrentContent,
		func(ctx context.Context, q *dao.Query) query.SubQuery {
			return query.GenericSubQuery[dao.ITorrentContentDo]{
				SubQuery: q.TorrentContent.WithContext(catalogue.Context(ctx)).ReadDB().
					Where(field.NewUnsafeFieldRaw(catalogue.VisibleInfoHash("torrent_contents.info_hash"))),
			}
		},
	)
}

func TorrentContentDefaultOption() query.Option {
	return query.Options(
		query.DefaultOption(),
		TorrentContentDefaultHydrate(),
		TorrentContentCoreJoins(),
		query.OrderBy(
			query.OrderByColumn{
				OrderByColumn: clause.OrderByColumn{
					Column: clause.Column{
						Table: clause.CurrentTable,
						Name:  colPublishedAt,
					},
					Desc: true,
				},
			},
		),
	)
}

func TorrentContentCoreJoins() query.Option {
	return query.Options(
		query.Join(func(q *dao.Query) []query.TableJoin {
			return []query.TableJoin{
				{
					Table: q.Torrent,
					On: []field.Expr{
						q.TorrentContent.InfoHash.EqCol(q.Torrent.InfoHash),
					},
					Type: query.TableJoinTypeInner,
				},
				{
					Table: q.Content,
					On: []field.Expr{
						q.TorrentContent.ContentType.EqCol(q.Content.Type),
						q.TorrentContent.ContentSource.EqCol(q.Content.Source),
						q.TorrentContent.ContentID.EqCol(q.Content.ID),
					},
					Type: query.TableJoinTypeLeft,
				},
			}
		}),
		ContentCoreJoins(),
	)
}

func TorrentContentDefaultHydrate() query.Option {
	return query.Options(
		HydrateTorrentContentTorrent(),
		HydrateTorrentContentContent(),
	)
}
