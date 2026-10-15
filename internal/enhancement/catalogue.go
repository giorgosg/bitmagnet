package enhancement

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/jackc/pgx/v5"
)

// LoadEvidence accepts only a read-only transaction created by the command. It
// excludes exact pending/banned decisions using the same policy as catalogue search.
// An explicit hash is never gated by prior enhancement state.
func LoadEvidence(ctx context.Context, tx pgx.Tx, hashes []protocol.ID, sample int) ([]Evidence, error) {
	if len(hashes) > 100 || sample > 100 || sample < 0 || (len(hashes) == 0 && sample == 0) ||
		(len(hashes) > 0 && sample > 0) {
		return nil, errors.New("select either 1-100 hashes or a sample of 1-100 torrents")
	}

	values := make([][]byte, 0, len(hashes))
	for _, h := range hashes {
		values = append(values, h.Bytes())
	}

	limit := len(hashes)
	if sample > 0 {
		limit = sample
	}

	rows, err := tx.Query(
		ctx,
		`SELECT info_hash,name,size,files_count,files_status FROM torrents WHERE `+catalogue.VisibleInfoHash(
			"torrents.info_hash",
		)+` AND (cardinality($1::bytea[])=0 OR info_hash=ANY($1::bytea[])) ORDER BY info_hash LIMIT $2`,
		values,
		limit,
	)
	if err != nil {
		return nil, err
	}

	var torrents []model.Torrent

	for rows.Next() {
		var (
			t      model.Torrent
			raw    []byte
			count  *int64
			size   int64
			status string
		)
		if err = rows.Scan(&raw, &t.Name, &size, &count, &status); err != nil {
			rows.Close()
			return nil, err
		}

		t.InfoHash, err = protocol.NewIDFromByteSlice(raw)
		if err != nil {
			rows.Close()
			return nil, err
		}

		t.Size = uint(size)

		t.FilesStatus = model.FilesStatus(status)
		if count != nil {
			t.FilesCount = model.NewNullUint(uint(*count))
		}

		torrents = append(torrents, t)
	}

	err = rows.Err()
	rows.Close()

	if err != nil {
		return nil, err
	}

	evidence := make([]Evidence, 0, len(torrents))
	for _, t := range torrents {
		// Bound loading as well as prompt size. Rank a directory's largest relevant
		// file first, then fill by relevance/size, exactly as BuildEvidence does.
		// One overflow row preserves the incomplete-layout flag when the stored set is huge.
		files, fileErr := tx.Query(ctx, `SELECT path,size FROM (SELECT path,size,
 row_number() OVER (PARTITION BY regexp_replace(path, '[^/]*$', '') ORDER BY
 (lower(path) ~ '\.(mkv|mp4|avi|m4v|iso|epub|pdf|flac|mp3|exe|apk|dmg|zip|rar|cbz|cbr)$') DESC,size DESC,path)
 AS directory_rank
 FROM torrent_files WHERE info_hash=$1) AS ranked ORDER BY (directory_rank=1) DESC,
 (lower(path) ~ '\.(mkv|mp4|avi|m4v|iso|epub|pdf|flac|mp3|exe|apk|dmg|zip|rar|cbz|cbr)$') DESC,size DESC,path
 LIMIT 21`, t.InfoHash.Bytes())
		if fileErr != nil {
			return nil, fileErr
		}

		for files.Next() {
			var (
				f    model.TorrentFile
				size int64
			)
			if err = files.Scan(&f.Path, &size); err != nil {
				files.Close()
				return nil, err
			}

			f.Size = uint(size)
			t.Files = append(t.Files, f)
		}

		err = files.Err()
		files.Close()

		if err != nil {
			return nil, err
		}

		// Both entry points project the durable ordinary snapshot, never their own
		// previous enhancement. Legacy rows remain readable without creating state.
		var ordinary string

		err = tx.QueryRow(ctx, `SELECT ordinary_output FROM classification_evaluations
 WHERE info_hash=$1 AND stage='ordinary'`, t.InfoHash.Bytes()).
			Scan(&ordinary)
		switch {
		case err == nil:
			tc, decodeErr := decodeOrdinary(ordinary)
			if decodeErr != nil {
				return nil, decodeErr
			}

			t.Contents = []model.TorrentContent{tc}
		case errors.Is(err, pgx.ErrNoRows):
			var classification []byte

			err = tx.QueryRow(ctx, `SELECT coalesce(jsonb_agg(jsonb_build_object(
 'contentType',content_type,'contentSource',content_source,'contentId',content_id,
 'inferredTitle',inferred_title,'inferredYear',inferred_year,
 'content',jsonb_build_object('title',coalesce(c.title,''),'releaseYear',coalesce(c.release_year,0)),
 'languages',languages,'episodes',episodes,'videoResolution',video_resolution,
 'videoSource',video_source,'videoCodec',video_codec,'video3D',video_3d,
 'videoModifier',video_modifier,'releaseGroup',release_group) ORDER BY tc.id),'[]'::jsonb)
 FROM torrent_contents tc LEFT JOIN content c ON c.type=tc.content_type
 AND c.source=tc.content_source AND c.id=tc.content_id WHERE tc.info_hash=$1`, t.InfoHash.Bytes()).Scan(&classification)
			if err != nil {
				return nil, err
			}

			// Generated model.Date fields are absent from this explicit legacy projection.
			//nolint:musttag
			if err = json.Unmarshal(classification, &t.Contents); err != nil {
				return nil, err
			}
		default:
			return nil, err
		}

		var hint []byte

		err = tx.QueryRow(ctx, `SELECT coalesce((SELECT jsonb_build_object(
 'contentType',content_type,'contentSource',content_source,'contentId',content_id,
 'title',title,'releaseYear',release_year,'languages',languages,'episodes',episodes,
 'videoResolution',video_resolution,'videoSource',video_source,'videoCodec',video_codec,
 'video3D',video_3d,'videoModifier',video_modifier,'releaseGroup',release_group)
 FROM torrent_hints WHERE info_hash=$1),'{}'::jsonb)`, t.InfoHash.Bytes()).Scan(&hint)
		if err != nil {
			return nil, err
		}

		if err = json.Unmarshal(hint, &t.Hint); err != nil {
			return nil, err
		}

		e, buildErr := BuildEvidence(t)
		if buildErr != nil {
			return nil, buildErr
		}

		evidence = append(evidence, e)
	}

	return evidence, nil
}
