package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// TasteConfig controls local, on-demand analysis. It never enables collection
// from browsers or external services and contains no third-party credentials.
type TasteConfig struct {
	Enabled bool            `json:"enabled"`
	Sources map[string]bool `json:"sources"`
	Version int64           `json:"version"`
}
type TasteSource struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	Records int    `json:"records"`
	Works   int    `json:"works"`
}
type TasteFacet struct {
	Kind          string   `json:"kind"`
	Key           string   `json:"key"`
	Label         string   `json:"label"`
	Weight        float64  `json:"weight"`
	EvidenceCount int      `json:"evidence_count"`
	Sources       []string `json:"sources"`
}
type TasteReason struct {
	Kind    string   `json:"kind"`
	Label   string   `json:"label"`
	Source  string   `json:"source"`
	Weight  float64  `json:"weight"`
	WorkIDs []string `json:"work_ids"`
}
type TasteRecommendation struct {
	Work    Work          `json:"work"`
	Score   float64       `json:"score"`
	Reasons []TasteReason `json:"reasons"`
}
type TasteReport struct {
	Config  TasteConfig           `json:"config"`
	Sources []TasteSource         `json:"sources"`
	Facets  []TasteFacet          `json:"facets"`
	Items   []TasteRecommendation `json:"items"`
	Total   int                   `json:"total"`
	Limit   int                   `json:"limit"`
	Offset  int                   `json:"offset"`
}

var tasteSourceLabels = []struct{ key, label string }{
	{"collection", "馆藏与收藏"}, {"history", "播放记录"}, {"feedback", "评分与反馈"}, {"follows", "本地演员关注"},
	{"browser", "已导入浏览器资料"},
}

func initializeTaste(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS catalog_taste_config(
 id INTEGER PRIMARY KEY CHECK(id=1),enabled INTEGER NOT NULL DEFAULT 0,
 sources TEXT NOT NULL CHECK(json_valid(sources)),version INTEGER NOT NULL DEFAULT 1
);
INSERT OR IGNORE INTO catalog_taste_config(id,enabled,sources,version)
 VALUES(1,0,'{"collection":true,"history":true,"feedback":true,"follows":true,"browser":false}',1);
UPDATE catalog_taste_config SET sources=json_set(sources,'$.browser',json('false')),version=version+1 WHERE json_type(sources,'$.browser') IS NULL;
CREATE TABLE IF NOT EXISTS catalog_actor_follows(
 actor_id TEXT PRIMARY KEY REFERENCES catalog_actors(id) ON DELETE CASCADE,
 following INTEGER NOT NULL CHECK(following IN (0,1)),version INTEGER NOT NULL,updated_at TEXT NOT NULL
);`)
	if err != nil {
		return err
	}
	return initializeBrowser(tx)
}

func readTasteConfig(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (TasteConfig, error) {
	var config TasteConfig
	var raw string
	if err := db.QueryRowContext(ctx, "SELECT enabled,sources,version FROM catalog_taste_config WHERE id=1").Scan(&config.Enabled, &raw, &config.Version); err != nil {
		return config, err
	}
	if err := json.Unmarshal([]byte(raw), &config.Sources); err != nil {
		return config, err
	}
	if !validTasteSources(config.Sources) {
		return config, fmt.Errorf("invalid stored taste source configuration")
	}
	return config, nil
}
func validTasteSources(sources map[string]bool) bool {
	if len(sources) != len(tasteSourceLabels) {
		return false
	}
	for _, source := range tasteSourceLabels {
		if _, ok := sources[source.key]; !ok {
			return false
		}
	}
	return true
}
func (s *Store) TasteConfig(ctx context.Context) (TasteConfig, error) {
	return readTasteConfig(ctx, s.db)
}
func (s *Store) SetTasteConfig(ctx context.Context, config TasteConfig) (TasteConfig, error) {
	if config.Version < 1 || !validTasteSources(config.Sources) {
		return TasteConfig{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE catalog_taste_config SET enabled=?,sources=?,version=version+1 WHERE id=1 AND version=?", config.Enabled, jsonText(config.Sources), config.Version)
	if err != nil {
		return TasteConfig{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return TasteConfig{}, err
	}
	if changed == 0 {
		return TasteConfig{}, ErrConflict
	}
	config.Version++
	return config, nil
}
func (s *Store) ResetTaste(ctx context.Context, version int64, confirmation string) (TasteConfig, error) {
	if confirmation != "清除偏好" {
		return TasteConfig{}, ErrInvalid
	}
	sources := map[string]bool{}
	for _, source := range tasteSourceLabels {
		sources[source.key] = false
	}
	// No derived profile or recommendations are persisted. Turning off every
	// input makes the next read empty, without destroying original personal data.
	return s.SetTasteConfig(ctx, TasteConfig{Enabled: false, Sources: sources, Version: version})
}

type ActorFollow struct {
	Following bool  `json:"following"`
	Version   int64 `json:"version"`
}

func (s *Store) SetActorFollow(ctx context.Context, id string, following bool, version int64) (ActorFollow, error) {
	if version < 0 || len(id) > 100 {
		return ActorFollow{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActorFollow{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM catalog_actors WHERE id=?", id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ActorFollow{}, ErrNotFound
	} else if err != nil {
		return ActorFollow{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_actor_follows(actor_id,following,version,updated_at)
 SELECT ?,?,1,? WHERE ?=0
 ON CONFLICT(actor_id) DO UPDATE SET following=excluded.following,version=catalog_actor_follows.version+1,updated_at=excluded.updated_at
 WHERE catalog_actor_follows.version=?`, id, following, timestamp(), version, version)
	if err != nil {
		return ActorFollow{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ActorFollow{}, err
	}
	if changed == 0 && version > 0 {
		result, err = tx.ExecContext(ctx, "UPDATE catalog_actor_follows SET following=?,version=version+1,updated_at=? WHERE actor_id=? AND version=?", following, timestamp(), id, version)
		if err != nil {
			return ActorFollow{}, err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return ActorFollow{}, err
		}
	}
	if changed != 1 {
		return ActorFollow{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return ActorFollow{}, err
	}
	return ActorFollow{Following: following, Version: version + 1}, nil
}

type tasteWork struct {
	Work            Work
	Actors          []Actor
	History         int
	BrowserHistory  bool
	BrowserBookmark bool
}

func (s *Store) Taste(ctx context.Context, limit, offset int) (TasteReport, error) {
	report := TasteReport{Sources: []TasteSource{}, Facets: []TasteFacet{}, Items: []TasteRecommendation{}, Limit: limit, Offset: offset}
	if limit < 1 || limit > 100 || offset < 0 {
		return report, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	report.Config, err = readTasteConfig(ctx, tx)
	if err != nil {
		return report, err
	}
	// One consistent database snapshot covers config, evidence, aliases and work
	// metadata. There is no network access, filesystem traversal or cached profile.
	var collection, historyRecords, historyWorks, feedback, follows int
	if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM catalog_works w WHERE EXISTS(SELECT 1 FROM catalog_assets a WHERE a.work_id=w.id)),
 (SELECT COUNT(*) FROM catalog_history WHERE position>0),
 (SELECT COUNT(DISTINCT a.work_id) FROM catalog_history h JOIN catalog_assets a ON a.id=h.asset_id WHERE h.position>0),
 (SELECT COUNT(*) FROM catalog_work_notes WHERE rating>0 OR feedback<>''),
 (SELECT COUNT(*) FROM catalog_actor_follows WHERE following=1)`).Scan(&collection, &historyRecords, &historyWorks, &feedback, &follows); err != nil {
		return report, err
	}
	var browserRecords, browserWorks int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(DISTINCT CASE WHEN e.code<>'' AND EXISTS(SELECT 1 FROM catalog_works w WHERE w.code=e.code) THEN e.code END) FROM catalog_browser_entries e`).Scan(&browserRecords, &browserWorks); err != nil {
		return report, err
	}
	counts := []int{collection, historyRecords, feedback, follows, browserRecords}
	works := []int{collection, historyWorks, feedback, 0, browserWorks}
	for i, source := range tasteSourceLabels {
		report.Sources = append(report.Sources, TasteSource{Key: source.key, Label: source.label, Enabled: report.Config.Enabled && report.Config.Sources[source.key], Records: counts[i], Works: works[i]})
	}
	if !report.Config.Enabled {
		return report, tx.Commit()
	}
	all, err := loadTasteWorks(ctx, tx)
	if err != nil {
		return report, err
	}
	followed, err := loadFollowedActors(ctx, tx)
	if err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	report.Facets, report.Items, err = rankTaste(ctx, all, followed, report.Config.Sources)
	if err != nil {
		return report, err
	}
	report.Total = len(report.Items)
	start := min(offset, report.Total)
	report.Items = report.Items[start:min(start+limit, report.Total)]
	// Facets are an overview, not a complete export of private personal history.
	if len(report.Facets) > 80 {
		report.Facets = report.Facets[:80]
	}
	return report, nil
}

func loadTasteWorks(ctx context.Context, tx *sql.Tx) ([]tasteWork, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+workColumns+" FROM catalog_works w LEFT JOIN catalog_personal p ON p.work_id=w.id WHERE EXISTS(SELECT 1 FROM catalog_assets a WHERE a.work_id=w.id) ORDER BY w.id")
	if err != nil {
		return nil, err
	}
	all := []tasteWork{}
	index := map[string]int{}
	for rows.Next() {
		work, err := scanWork(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		index[work.ID] = len(all)
		all = append(all, tasteWork{Work: work})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT wa.work_id,a.id,a.display_name FROM catalog_work_actors wa JOIN catalog_actors a ON a.id=wa.actor_id ORDER BY wa.work_id,a.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var a Actor
		if err := rows.Scan(&id, &a.ID, &a.DisplayName); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[id]; ok {
			all[i].Actors = append(all[i].Actors, a)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT a.work_id,COUNT(*) FROM catalog_history h JOIN catalog_assets a ON a.id=h.asset_id WHERE h.position>0 GROUP BY a.work_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		if i, ok := index[id]; ok {
			all[i].History = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT w.id,MAX(s.kind='history'),MAX(s.kind='bookmarks') FROM catalog_browser_entries e JOIN catalog_browser_sources s ON s.id=e.source_id JOIN catalog_works w ON w.code=e.code WHERE e.code<>'' GROUP BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var history, bookmark bool
		if err := rows.Scan(&id, &history, &bookmark); err != nil {
			return nil, err
		}
		if i, ok := index[id]; ok {
			all[i].BrowserHistory = history
			all[i].BrowserBookmark = bookmark
		}
	}
	return all, rows.Err()
}
func loadFollowedActors(ctx context.Context, tx *sql.Tx) ([]Actor, error) {
	rows, err := tx.QueryContext(ctx, "SELECT a.id,a.display_name FROM catalog_actor_follows f JOIN catalog_actors a ON a.id=f.actor_id WHERE f.following=1 ORDER BY a.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Actor{}
	for rows.Next() {
		var a Actor
		if err := rows.Scan(&a.ID, &a.DisplayName); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func sortedTasteSources(values map[string]bool) []string {
	result := []string{}
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
