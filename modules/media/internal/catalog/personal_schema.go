package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
)

func initializePersonal(tx *sql.Tx) error {
	const schema = `
CREATE TABLE IF NOT EXISTS catalog_actors(
 id TEXT PRIMARY KEY,name TEXT NOT NULL,display_name TEXT NOT NULL,biography TEXT NOT NULL DEFAULT '',
 aliases TEXT NOT NULL DEFAULT '[]',version INTEGER NOT NULL DEFAULT 1,
 portrait BLOB,portrait_mime TEXT NOT NULL DEFAULT '',portrait_revision INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS catalog_state(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS catalog_actor_names(
 name_key TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES catalog_actors(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS catalog_work_actors(
 work_id TEXT NOT NULL REFERENCES catalog_works(id) ON DELETE CASCADE,
 actor_id TEXT NOT NULL REFERENCES catalog_actors(id) ON DELETE CASCADE,PRIMARY KEY(work_id,actor_id)
);
CREATE INDEX IF NOT EXISTS catalog_work_actor_lookup ON catalog_work_actors(actor_id,work_id);
CREATE TABLE IF NOT EXISTS catalog_history(
 id TEXT PRIMARY KEY,asset_id TEXT NOT NULL REFERENCES catalog_assets(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL,position REAL NOT NULL,duration REAL NOT NULL,completed INTEGER NOT NULL,
 started_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(asset_id,session_id)
);
CREATE INDEX IF NOT EXISTS catalog_history_recent ON catalog_history(updated_at DESC,id);
CREATE TABLE IF NOT EXISTS catalog_work_notes(
 work_id TEXT PRIMARY KEY REFERENCES catalog_works(id) ON DELETE CASCADE,
 watched INTEGER,rating INTEGER NOT NULL DEFAULT 0 CHECK(rating BETWEEN 0 AND 10),
 feedback TEXT NOT NULL DEFAULT '' CHECK(feedback IN ('','like','dislike')),revision INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS catalog_lists(
 id TEXT PRIMARY KEY,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS catalog_list_items(
 list_id TEXT NOT NULL REFERENCES catalog_lists(id) ON DELETE CASCADE,
 work_id TEXT NOT NULL REFERENCES catalog_works(id) ON DELETE CASCADE,
 position INTEGER NOT NULL,PRIMARY KEY(list_id,work_id),UNIQUE(list_id,position)
);`
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	// Legacy progress has no session boundary; retain one clearly legacy record,
	// never invent multiple watches from a single last-position measurement.
	var initialized int
	if err := tx.QueryRow("SELECT COUNT(*) FROM catalog_state WHERE key='history_backfill_1'").Scan(&initialized); err != nil {
		return err
	}
	if initialized == 0 {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO catalog_history(id,asset_id,session_id,position,duration,completed,started_at,updated_at)
 SELECT 'legacy-'||asset_id,asset_id,'legacy',position,duration,completed,played_at,played_at FROM catalog_progress
 WHERE position>0 AND NOT EXISTS(SELECT 1 FROM catalog_history h WHERE h.asset_id=catalog_progress.asset_id)`); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO catalog_state(key,value) VALUES('history_backfill_1','done')"); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO catalog_work_notes(work_id,watched,revision) SELECT DISTINCT work_id,1,1 FROM catalog_assets a JOIN catalog_progress g ON g.asset_id=a.id WHERE g.completed=1`); err != nil {
			return err
		}
	}
	return syncActorsTx(context.Background(), tx)
}

// Rebuild the small relation index from authoritative work metadata in the same
// publication transaction. Existing profiles, aliases and portraits are untouched.
func syncActorsTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,actors FROM catalog_works WHERE EXISTS(SELECT 1 FROM catalog_assets a WHERE a.work_id=catalog_works.id)`)
	if err != nil {
		return err
	}
	type row struct {
		id    string
		names []string
	}
	works := []row{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var names []string
		if err := json.Unmarshal([]byte(raw), &names); err != nil {
			rows.Close()
			return err
		}
		works = append(works, row{id, names})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_work_actors"); err != nil {
		return err
	}
	for _, work := range works {
		for _, name := range work.names {
			key := actorNameKey(name)
			if !validActorName(name) {
				continue
			}
			id := stableID("actor", key)
			if err := tx.QueryRowContext(ctx, "SELECT actor_id FROM catalog_actor_names WHERE name_key=?", key).Scan(&id); err != nil && err != sql.ErrNoRows {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO catalog_actors(id,name,display_name) VALUES(?,?,?)", id, name, name); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO catalog_actor_names(name_key,actor_id) VALUES(?,?)", key, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO catalog_work_actors(work_id,actor_id) VALUES(?,?)", work.id, id); err != nil {
				return err
			}
		}
	}
	return nil
}
