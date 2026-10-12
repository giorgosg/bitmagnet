package catalogue_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
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
