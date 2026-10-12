package dbtest

import (
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
)

// ModerationCatalogue seeds harmless metadata spanning each visibility state.
// Every name, identifier and file path is synthetic, including on seeded databases.
func ModerationCatalogue(t *testing.T, db *DB) []protocol.ID {
	t.Helper()

	hashes := []protocol.ID{{1}, {2}, {3}, {4}}

	service := moderation.NewService(db.Gorm)
	for i, name := range []string{"Visible", "Pending", "Allowed", "Banned"} {
		_, err := db.Pool.Exec(
			t.Context(),
			`INSERT INTO torrents (info_hash,name,size,private,created_at,
updated_at)
VALUES ($1,$2,1024,false,now(),now())`,
			hashes[i][:],
			name,
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO torrent_files (info_hash,index,path,size,created_at,
updated_at)
VALUES ($1,0,$2,1024,now(),now())`,
			hashes[i][:],
			name+"/harmless.txt",
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO torrent_contents (info_hash,content_type,created_at,
updated_at)
VALUES ($1,'ebook',now(),now())`,
			hashes[i][:],
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO torrent_sources (key,name,created_at,updated_at) VALUES ($1,$2,now(),now())`,
			strings.ToLower(name),
			name+" source",
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO torrents_torrent_sources (source,info_hash,created_at,
updated_at)
VALUES ($1,$2,now(),now())`,
			strings.ToLower(name),
			hashes[i][:],
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO content (type,source,id,title,release_year,created_at,
updated_at)
VALUES ('ebook','tmdb',$1,$2,$3,now(),now())`,
			strings.ToLower(name),
			name+" book",
			2000+i,
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`UPDATE torrent_contents SET content_source='tmdb',content_id=$1 WHERE info_hash=$2`,
			strings.ToLower(name),
			hashes[i][:],
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO content_collections (type,source,id,name,created_at,
updated_at)
VALUES ('genre','tmdb',$1,$2,now(),now())`,
			strings.ToLower(name),
			name+" genre",
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO content_collections_content VALUES ('ebook','tmdb',$1,'genre','tmdb',$1)`,
			strings.ToLower(name),
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = db.Pool.Exec(
			t.Context(),
			`INSERT INTO torrent_tags (info_hash,name,created_at,updated_at) VALUES ($1,$2,now(),now())`,
			hashes[i][:],
			strings.ToLower(name),
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := service.Flag(
		t.Context(),
		moderation.IdentifierForInfoHash(hashes[1]),
		moderation.Evidence{InfoHash: &hashes[1], Name: "Pending", Files: []string{"Pending/harmless.txt"}},
		"reviewer",
	); err != nil {
		t.Fatal(err)
	}

	if err := service.Allow(t.Context(), moderation.IdentifierForInfoHash(hashes[2]), "reviewer"); err != nil {
		t.Fatal(err)
	}
	// A digest-only imported ban must conceal metadata even without retained infohash evidence.
	if err := service.ApplyImport(
		t.Context(),
		moderation.IdentifierForInfoHash(hashes[3]),
		moderation.Moderator,
		"synthetic",
	); err != nil {
		t.Fatal(err)
	}

	return hashes
}
