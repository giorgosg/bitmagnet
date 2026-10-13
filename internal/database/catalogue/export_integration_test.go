package catalogue_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestExportConcealsModerationAndRestoresCatalogue(t *testing.T) {
	t.Parallel()
	source := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, source)

	const unusual = "Harmless 'quoted' \\ path\nΕλληνικά"

	_, err := source.Pool.Exec(t.Context(), `UPDATE torrents SET name=$1 WHERE info_hash=$2`, unusual, hashes[0][:])
	require.NoError(t, err)
	_, err = source.Pool.Exec(
		t.Context(),
		`INSERT INTO key_values (key,value,created_at,
updated_at)
VALUES ('private-auth-marker','sensitive internal value',now(),now())`,
	)
	require.NoError(t, err)
	conn, err := pgx.Connect(t.Context(), source.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close(t.Context())) })
	// Export is portable even if its source uses non-default SQL string escaping.
	_, err = conn.Exec(t.Context(), `SET standard_conforming_strings = off`)
	require.NoError(t, err)

	var output bytes.Buffer
	require.NoError(t, catalogue.Export(t.Context(), conn, &output))

	for _, private := range []string{
		"Pending", "Banned", "moderation_", "private-auth-marker", "sensitive internal value",
	} {
		require.NotContains(t, output.String(), private)
	}

	destination := dbtest.New(t)
	_, err = destination.Pool.Exec(t.Context(), output.String())
	require.NoError(t, err, "column INSERT data must restore through PostgreSQL")
	// Restoration is idempotent, including all dependent foreign-key rows.
	_, err = destination.Pool.Exec(t.Context(), output.String())
	require.NoError(t, err)

	var names []string

	rows, err := destination.Pool.Query(t.Context(), `SELECT name FROM torrents ORDER BY info_hash`)
	require.NoError(t, err)

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}

	rows.Close()
	require.NoError(t, rows.Err())
	require.Equal(t, []string{unusual, "Allowed"}, names)

	var files int
	require.NoError(t, destination.Pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM torrent_files`).Scan(&files))
	require.Equal(t, 2, files)

	var nullable bool
	require.NoError(
		t,
		destination.Pool.QueryRow(
			t.Context(), `SELECT extension IS NULL FROM torrents WHERE info_hash=$1`, hashes[0][:],
		).
			Scan(&nullable),
	)
	require.True(t, nullable)
}

func TestExportStreamsMultipleInsertBatches(t *testing.T) {
	t.Parallel()
	source := dbtest.New(t)
	_, err := source.Pool.Exec(t.Context(), `INSERT INTO torrents(info_hash,name,size,private,created_at,updated_at)
 SELECT decode(lpad(to_hex(i),40,'0'),'hex'),'Synthetic volume '||i,1024,false,now(),now()
 FROM generate_series(1,1002) AS i`)
	require.NoError(t, err)
	conn, err := pgx.Connect(t.Context(), source.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close(t.Context())) })

	var output bytes.Buffer
	require.NoError(t, catalogue.Export(t.Context(), conn, &output))
	require.Equal(t, 2, strings.Count(output.String(), `INSERT INTO "public"."torrents"`))
	destination := dbtest.New(t)
	_, err = destination.Pool.Exec(t.Context(), output.String())
	require.NoError(t, err)

	var count int
	require.NoError(t, destination.Pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM torrents`).Scan(&count))
	require.Equal(t, 1002, count)
}

func TestExportKeepsDatesAcrossDateStyleSettings(t *testing.T) {
	t.Parallel()
	source := dbtest.New(t)
	dbtest.ModerationCatalogue(t, source)
	_, err := source.Pool.Exec(t.Context(), `UPDATE content SET release_date=DATE '2000-02-03' WHERE id='visible'`)
	require.NoError(t, err)
	conn, err := pgx.Connect(t.Context(), source.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close(t.Context())) })
	_, err = conn.Exec(t.Context(), `SET DateStyle = 'SQL, DMY'`)
	require.NoError(t, err)

	var output bytes.Buffer
	require.NoError(t, catalogue.Export(t.Context(), conn, &output))
	destination := dbtest.New(t)
	_, err = destination.Pool.Exec(t.Context(), "SET DateStyle = 'ISO, MDY';\n"+output.String())
	require.NoError(t, err)

	var sameDate bool
	require.NoError(
		t,
		destination.Pool.QueryRow(t.Context(), `SELECT release_date=DATE '2000-02-03' FROM content WHERE id='visible'`).
			Scan(&sameDate),
	)
	require.True(t, sameDate, "portable SQL must preserve day/month meaning across session settings")
}

func TestExportPreservesHintSourceDependencies(t *testing.T) {
	t.Parallel()
	source := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, source)

	hashes = append(hashes, protocol.ID{5}, protocol.ID{6})
	for _, hash := range hashes[4:] {
		_, err := source.Pool.Exec(t.Context(), `INSERT INTO torrents
 (info_hash,name,size,private,created_at,updated_at)
 VALUES ($1,'Synthetic hint torrent',1024,false,now(),now())`, hash[:])
		require.NoError(t, err)
	}

	require.NoError(t, moderation.NewService(source.Gorm).ApplyImport(
		t.Context(), moderation.IdentifierForInfoHash(hashes[5]), moderation.Moderator, "synthetic",
	))

	for _, key := range []string{"unused", "visible", "allowed", "mixed", "pending", "banned"} {
		_, err := source.Pool.Exec(t.Context(), `INSERT INTO metadata_sources(key,name,created_at,updated_at)
 VALUES ($1,$1,now(),now())`, "synthetic-hint-"+key)
		require.NoError(t, err)
	}

	for _, hint := range []struct {
		key   string
		index int
	}{
		{"visible", 0}, {"allowed", 2}, {"mixed", 4}, {"mixed", 3}, {"pending", 1}, {"banned", 5},
	} {
		_, err := source.Pool.Exec(t.Context(), `INSERT INTO torrent_hints
 (info_hash,content_type,content_source,content_id,created_at,updated_at)
 VALUES ($1,'ebook',$2,'synthetic-hint-id',now(),now())`,
			hashes[hint.index][:], "synthetic-hint-"+hint.key)
		require.NoError(t, err)
	}

	conn, err := pgx.Connect(t.Context(), source.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close(t.Context())) })

	var output bytes.Buffer
	require.NoError(t, catalogue.Export(t.Context(), conn, &output))
	destination := dbtest.New(t)
	_, err = destination.Pool.Exec(t.Context(), output.String())
	require.NoError(t, err, "visible hints must restore with their metadata source dependency")

	var keys []string

	rows, err := destination.Pool.Query(t.Context(), `SELECT key FROM metadata_sources
 WHERE key LIKE 'synthetic-hint-%' ORDER BY key`)
	require.NoError(t, err)

	for rows.Next() {
		var key string
		require.NoError(t, rows.Scan(&key))
		keys = append(keys, key)
	}

	rows.Close()
	require.NoError(t, rows.Err())
	require.Equal(t, []string{
		"synthetic-hint-allowed", "synthetic-hint-mixed", "synthetic-hint-unused", "synthetic-hint-visible",
	}, keys)

	var count int
	require.NoError(t, destination.Pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM torrent_hints`).Scan(&count))
	require.Equal(t, 3, count)
}

func TestExportKeepsOnlyVisibleMetadataSourceDependencies(t *testing.T) {
	t.Parallel()
	source := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, source)

	var expected []string

	for _, kind := range []string{"attribute", "collection", "content"} {
		for _, state := range []string{"hidden", "mixed", "visible"} {
			key := "synthetic-" + kind + "-" + state
			_, err := source.Pool.Exec(
				t.Context(),
				`INSERT INTO metadata_sources(key,name,created_at,updated_at)
 VALUES ($1,$1,now(),now())`,
				key,
			)
			require.NoError(t, err)

			indices := []int{3}

			switch state {
			case "mixed":
				indices = []int{0, 3}
			case "visible":
				indices = []int{0}
			}

			for _, index := range indices {
				id := []string{"visible", "pending", "allowed", "banned"}[index]

				switch kind {
				case "content":
					_, err = source.Pool.Exec(t.Context(), `INSERT INTO content
 (type,source,id,title,created_at,updated_at) VALUES ('ebook',$1,$2,'Synthetic metadata',now(),now())`, key, id)
					require.NoError(t, err)
					_, err = source.Pool.Exec(t.Context(), `INSERT INTO torrent_contents
 (info_hash,content_type,content_source,content_id,created_at,updated_at)
 VALUES ($1,'ebook',$2,$3,now(),now())`, hashes[index][:], key, id)
				case "attribute":
					_, err = source.Pool.Exec(t.Context(), `INSERT INTO content_attributes
 (content_type,content_source,content_id,source,key,value,created_at,updated_at)
 VALUES ('ebook','tmdb',$1,$2,'synthetic','harmless',now(),now())`, id, key)
				case "collection":
					_, err = source.Pool.Exec(t.Context(), `INSERT INTO content_collections
 (type,source,id,name,created_at,updated_at) VALUES ('genre',$1,$2,'Synthetic collection',now(),now())`, key, id)
					require.NoError(t, err)
					_, err = source.Pool.Exec(t.Context(), `INSERT INTO content_collections_content
 VALUES ('ebook','tmdb',$1,'genre',$2,$1)`, id, key)
				}

				require.NoError(t, err)
			}

			if state != "hidden" {
				expected = append(expected, key)
			}
		}
	}

	conn, err := pgx.Connect(t.Context(), source.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close(t.Context())) })

	var output bytes.Buffer
	require.NoError(t, catalogue.Export(t.Context(), conn, &output))
	destination := dbtest.New(t)
	_, err = destination.Pool.Exec(t.Context(), output.String())
	require.NoError(t, err)

	for _, key := range expected {
		var present bool
		require.NoError(t, destination.Pool.QueryRow(t.Context(),
			`SELECT EXISTS (SELECT 1 FROM metadata_sources WHERE key=$1)`, key).Scan(&present))
		require.True(t, present, "source %s has a visible dependency", key)
	}

	var count int
	require.NoError(t, destination.Pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM metadata_sources WHERE key LIKE 'synthetic-%'`).Scan(&count))
	require.Equal(t, len(expected), count, "sources with exclusively hidden dependencies must be omitted")
}
