package catalogue

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Export writes portable, data-only column INSERTs for the ordinary catalogue.
// It never exports auth, internal key/value state, or the private moderation ledger.
// The caller must discard partial output on error. A single read-only snapshot
// keeps decisions and related rows consistent without changing source data.
func Export(ctx context.Context, conn *pgx.Conn, output io.Writer) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("start catalogue export: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(
		ctx,
		`SET LOCAL bytea_output = 'hex'; SET LOCAL standard_conforming_strings = on;
 SET LOCAL DateStyle = 'ISO, YMD'; SET LOCAL extra_float_digits = 3`,
	); err != nil {
		return err
	}

	writer := bufio.NewWriter(output)
	if _, err = io.WriteString(
		writer,
		`-- Ordinary catalogue data; not a database backup or ban list.
BEGIN;
SET LOCAL standard_conforming_strings = on;
SET LOCAL client_encoding = 'UTF8';
SET LOCAL DateStyle = 'ISO, YMD';
`,
	); err != nil {
		return err
	}

	for _, table := range []string{
		"metadata_sources", "content", "content_attributes", "content_collections", "content_collections_content",
		"torrent_sources", "torrents", "torrent_files", "torrent_hints", "torrent_contents", "torrent_tags",
		"torrents_torrent_sources",
	} {
		if err = exportTable(ctx, tx, writer, table); err != nil {
			return fmt.Errorf("export catalogue table %s: %w", table, err)
		}
	}

	if _, err = io.WriteString(writer, "COMMIT;\n"); err != nil {
		return err
	}

	if err = writer.Flush(); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func exportTable(ctx context.Context, tx pgx.Tx, writer io.Writer, table string) error {
	columns, err := tx.Query(
		ctx,
		`SELECT column_name FROM information_schema.columns
WHERE table_schema='public' AND table_name=$1 AND is_generated='NEVER' ORDER BY ordinal_position`,
		table,
	)
	if err != nil {
		return err
	}

	var (
		names       []string
		expressions []string
	)

	for columns.Next() {
		var name string
		if err = columns.Scan(&name); err != nil {
			columns.Close()
			return err
		}

		quoted := pgx.Identifier{name}.Sanitize()
		names = append(names, quoted)
		expressions = append(expressions, "quote_nullable("+quoted+"::text)")
	}

	columns.Close()

	if err = columns.Err(); err != nil {
		return err
	}

	if len(names) == 0 {
		return fmt.Errorf("missing catalogue table %s", table)
	}

	selection := "SELECT " + strings.Join(expressions, ", ") + " FROM " +
		pgx.Identifier{"public", table}.Sanitize() + " WHERE " + exportPredicate(table)

	rows, err := tx.Query(ctx, selection)
	if err != nil {
		return err
	}
	defer rows.Close()

	const batchSize = 1000

	count := 0

	for rows.Next() {
		values := make([]string, len(names))

		destinations := make([]any, len(names))
		for i := range values {
			destinations[i] = &values[i]
		}

		if err = rows.Scan(destinations...); err != nil {
			return err
		}

		if count%batchSize == 0 {
			if _, err = fmt.Fprintf(
				writer,
				"INSERT INTO %s (%s) VALUES\n",
				pgx.Identifier{"public", table}.Sanitize(),
				strings.Join(names, ", "),
			); err != nil {
				return err
			}
		} else if _, err = io.WriteString(writer, ",\n"); err != nil {
			return err
		}

		if _, err = fmt.Fprintf(writer, "(%s)", strings.Join(values, ", ")); err != nil {
			return err
		}

		count++
		if count%batchSize == 0 {
			if _, err = io.WriteString(writer, " ON CONFLICT DO NOTHING;\n"); err != nil {
				return err
			}
		}
	}

	if err = rows.Err(); err != nil {
		return err
	}

	if count%batchSize != 0 {
		_, err = io.WriteString(writer, " ON CONFLICT DO NOTHING;\n")
	}

	return err
}
