package catalog

import "database/sql"

// Rebuild only the early path-unique asset table. Dependent progress is copied
// inside the same transaction before either old table is dropped. A failure at
// any point rolls the entire catalog schema and data back.
func migrateAssetVersions(tx *sql.Tx) error {
	rows, err := tx.Query("PRAGMA table_info(catalog_assets)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var index, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			return err
		}
		found = found || name == "file_identity"
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !found {
		const migration = `
CREATE TABLE catalog_assets_next (
 id TEXT PRIMARY KEY, library_id INTEGER NOT NULL REFERENCES catalog_libraries(id),
 work_id TEXT NOT NULL REFERENCES catalog_works(id), path TEXT NOT NULL,
 size INTEGER NOT NULL, modified_ns INTEGER NOT NULL, file_identity TEXT NOT NULL DEFAULT '', available INTEGER NOT NULL DEFAULT 1,
 nfo_path TEXT NOT NULL DEFAULT '', poster_path TEXT NOT NULL DEFAULT '',
 issues TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(issues)),
 subtitles TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(subtitles)),
 metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)), scan_token TEXT NOT NULL
);
INSERT INTO catalog_assets_next(id,library_id,work_id,path,size,modified_ns,available,nfo_path,poster_path,issues,subtitles,metadata,scan_token)
SELECT id,library_id,work_id,path,size,modified_ns,available,nfo_path,poster_path,issues,subtitles,metadata,scan_token FROM catalog_assets;
CREATE TABLE catalog_progress_next (
 asset_id TEXT PRIMARY KEY REFERENCES catalog_assets_next(id), position REAL NOT NULL,
 duration REAL NOT NULL, revision INTEGER NOT NULL, completed INTEGER NOT NULL, played_at TEXT NOT NULL
);
INSERT INTO catalog_progress_next SELECT asset_id,position,duration,revision,completed,played_at FROM catalog_progress;
DROP TABLE catalog_progress;
DROP TABLE catalog_assets;
ALTER TABLE catalog_assets_next RENAME TO catalog_assets;
ALTER TABLE catalog_progress_next RENAME TO catalog_progress;
`
		if _, err := tx.Exec(migration); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS catalog_assets_work ON catalog_assets(work_id,available);
CREATE INDEX IF NOT EXISTS catalog_assets_path ON catalog_assets(library_id,path,available);
CREATE INDEX IF NOT EXISTS catalog_progress_played ON catalog_progress(played_at DESC);`)
	return err
}
