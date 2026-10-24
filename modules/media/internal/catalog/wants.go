package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrAlreadyAcquired = errors.New("作品已经在馆藏中")

// Wants are acquisition intent, not watch-later flags or assetless catalog
// works. An acquisition receipt is written only by verified catalog publication.
type Want struct {
	ID          int64            `json:"id"`
	Code        string           `json:"code"`
	Title       string           `json:"title"`
	Studio      string           `json:"studio"`
	ReleaseDate string           `json:"release_date"`
	Actors      []string         `json:"actors"`
	Origin      string           `json:"origin"`
	State       string           `json:"state"`
	Phase       string           `json:"phase"`
	Version     int64            `json:"version"`
	LastJobID   int64            `json:"last_job_id"`
	Acquisition *WantAcquisition `json:"acquisition"`
	Available   bool             `json:"available"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
}

type WantAcquisition struct {
	AssetID       string `json:"asset_id"`
	WorkID        string `json:"work_id"`
	Source        string `json:"source"`
	Batch         string `json:"batch"`
	AcquiredAt    string `json:"acquired_at"`
	ProducerJobID int64  `json:"producer_job_id"`
}

type WantInput struct {
	Code        string   `json:"code"`
	Title       string   `json:"title"`
	ReleaseDate string   `json:"release_date"`
	Studio      string   `json:"studio"`
	Actors      []string `json:"actors"`
}

type WantPatch struct {
	Version     int64     `json:"version"`
	Title       *string   `json:"title"`
	ReleaseDate *string   `json:"release_date"`
	Studio      *string   `json:"studio"`
	Actors      *[]string `json:"actors"`
	State       *string   `json:"state"`
}

type WantFilter struct {
	Query, Phase  string
	Limit, Offset int
}
type WantPage struct {
	Items  []Want         `json:"items"`
	Counts map[string]int `json:"counts"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

func initializeWants(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS catalog_wants (
 id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '',
 release_date TEXT NOT NULL DEFAULT '', studio TEXT NOT NULL DEFAULT '', actors TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(actors)),
 origin TEXT NOT NULL DEFAULT 'manual', state TEXT NOT NULL DEFAULT 'wanted' CHECK(state IN ('wanted','given_up','acquired')),
 version INTEGER NOT NULL DEFAULT 1 CHECK(version>0),last_job_id INTEGER NOT NULL DEFAULT 0,
 acquisition TEXT NOT NULL DEFAULT 'null' CHECK(json_valid(acquisition)),created_at TEXT NOT NULL,updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_wants_state ON catalog_wants(state,id);
CREATE INDEX IF NOT EXISTS catalog_works_code ON catalog_works(code);`)
	return err
}

func wantPhase(w Want, today string) string {
	if w.State == "acquired" {
		return "acquired"
	}
	if w.State == "given_up" {
		return "given_up"
	}
	if w.ReleaseDate != "" && w.ReleaseDate > today {
		return "unreleased"
	}
	return "searching"
}

const wantColumns = `w.id,w.code,w.title,w.release_date,w.studio,w.actors,w.origin,w.state,w.version,w.last_job_id,w.acquisition,w.created_at,w.updated_at,
EXISTS(SELECT 1 FROM catalog_assets a WHERE a.id=json_extract(w.acquisition,'$.asset_id') AND a.work_id=json_extract(w.acquisition,'$.work_id') AND a.available=1)`

func scanWant(row rowScanner, today string) (Want, error) {
	var w Want
	var actors, acquisition string
	err := row.Scan(&w.ID, &w.Code, &w.Title, &w.ReleaseDate, &w.Studio, &actors, &w.Origin, &w.State, &w.Version, &w.LastJobID, &acquisition, &w.CreatedAt, &w.UpdatedAt, &w.Available)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	if err = json.Unmarshal([]byte(actors), &w.Actors); err != nil {
		return w, err
	}
	if err = json.Unmarshal([]byte(acquisition), &w.Acquisition); err != nil {
		return w, err
	}
	w.Phase = wantPhase(w, today)
	return w, nil
}

type wantReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getWant(ctx context.Context, db wantReader, id int64) (Want, error) {
	if id < 1 {
		return Want{}, ErrInvalid
	}
	return scanWant(db.QueryRowContext(ctx, "SELECT "+wantColumns+" FROM catalog_wants w WHERE w.id=?", id), time.Now().Format("2006-01-02"))
}
func (s *Store) Want(ctx context.Context, id int64) (Want, error) { return getWant(ctx, s.db, id) }

func wantText(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}
func validateWantInput(input WantInput) bool {
	if !wantText(input.Title, 1000) || !wantText(input.Studio, 300) || len(input.Actors) > 100 {
		return false
	}
	if input.ReleaseDate != "" {
		day, err := time.Parse("2006-01-02", input.ReleaseDate)
		if err != nil || day.Format("2006-01-02") != input.ReleaseDate {
			return false
		}
	}
	for _, actor := range input.Actors {
		if actor == "" || !wantText(actor, 200) {
			return false
		}
	}
	return true
}

func (s *Store) AddWant(ctx context.Context, input WantInput) (Want, bool, error) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	if len(input.Code) > 128 || input.Code == "" || s.code(input.Code) != input.Code || !validateWantInput(input) {
		return Want{}, false, ErrInvalid
	}
	if input.Actors == nil {
		input.Actors = []string{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Want{}, false, err
	}
	defer tx.Rollback()
	var inLibrary bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_works w JOIN catalog_assets a ON a.work_id=w.id WHERE w.code=? AND a.available=1)`, input.Code).Scan(&inLibrary); err != nil {
		return Want{}, false, err
	}
	if inLibrary {
		return Want{}, false, ErrAlreadyAcquired
	}
	var id int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM catalog_wants WHERE code=?", input.Code).Scan(&id)
	if err == nil {
		// Explicit re-add can reopen a historical acquisition whose asset is now
		// absent. Mere GETs and an offline mount never schedule another download.
		if _, err = tx.ExecContext(ctx, "UPDATE catalog_wants SET state='wanted',acquisition='null',version=version+1,updated_at=? WHERE id=? AND state='acquired'", timestamp(), id); err != nil {
			return Want{}, false, err
		}
		w, err := getWant(ctx, tx, id)
		if err != nil {
			return w, false, err
		}
		return w, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Want{}, false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_wants").Scan(&count); err != nil {
		return Want{}, false, err
	}
	if count >= 10000 {
		return Want{}, false, ErrInvalid
	}
	stamp := timestamp()
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_wants(code,title,release_date,studio,actors,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, input.Code, input.Title, input.ReleaseDate, input.Studio, jsonText(input.Actors), stamp, stamp)
	if err != nil {
		return Want{}, false, err
	}
	id, err = result.LastInsertId()
	if err != nil {
		return Want{}, false, err
	}
	w, err := getWant(ctx, tx, id)
	if err != nil {
		return w, false, err
	}
	return w, true, tx.Commit()
}

func (s *Store) PatchWant(ctx context.Context, id int64, patch WantPatch) (Want, error) {
	if patch.Version < 1 || (patch.Title == nil && patch.ReleaseDate == nil && patch.Studio == nil && patch.Actors == nil && patch.State == nil) {
		return Want{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Want{}, err
	}
	defer tx.Rollback()
	w, err := getWant(ctx, tx, id)
	if err != nil {
		return w, err
	}
	if w.Version != patch.Version {
		return w, ErrConflict
	}
	if patch.State != nil {
		if w.State == "acquired" || (*patch.State != "wanted" && *patch.State != "given_up") {
			return w, ErrInvalid
		}
		w.State = *patch.State
	}
	if patch.Title != nil {
		w.Title = *patch.Title
	}
	if patch.Studio != nil {
		w.Studio = *patch.Studio
	}
	if patch.ReleaseDate != nil {
		w.ReleaseDate = *patch.ReleaseDate
	}
	if patch.Actors != nil {
		w.Actors = *patch.Actors
	}
	if !validateWantInput(WantInput{Title: w.Title, Studio: w.Studio, ReleaseDate: w.ReleaseDate, Actors: w.Actors}) {
		return w, ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_wants SET title=?,studio=?,release_date=?,actors=?,state=?,version=version+1,updated_at=? WHERE id=? AND version=?`, w.Title, w.Studio, w.ReleaseDate, jsonText(w.Actors), w.State, timestamp(), id, patch.Version)
	if err != nil {
		return Want{}, err
	}
	w, err = getWant(ctx, tx, id)
	if err != nil {
		return w, err
	}
	return w, tx.Commit()
}

func (s *Store) RemoveWant(ctx context.Context, id, version int64) error {
	if id < 1 || version < 1 {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM catalog_wants WHERE id=? AND version=?", id, version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) Wants(ctx context.Context, filter WantFilter) (WantPage, error) {
	page := WantPage{Items: []Want{}, Counts: map[string]int{"searching": 0, "unreleased": 0, "given_up": 0, "acquired": 0}, Limit: filter.Limit, Offset: filter.Offset}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.Offset > 10000 || !wantText(filter.Query, 500) {
		return page, ErrInvalid
	}
	if filter.Phase != "" {
		if _, ok := page.Counts[filter.Phase]; !ok {
			return page, ErrInvalid
		}
	}
	today := time.Now().Format("2006-01-02")
	phaseSQL := `CASE WHEN w.state='acquired' THEN 'acquired' WHEN w.state='given_up' THEN 'given_up' WHEN w.release_date<>'' AND w.release_date>? THEN 'unreleased' ELSE 'searching' END`
	where := `(instr(lower(w.code),lower(?))>0 OR instr(lower(w.title),lower(?))>0 OR instr(lower(w.actors),lower(?))>0 OR instr(lower(w.studio),lower(?))>0)`
	args := []any{filter.Query, filter.Query, filter.Query, filter.Query}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT "+phaseSQL+",COUNT(*) FROM catalog_wants w WHERE "+where+" GROUP BY 1", append([]any{today}, args...)...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var phase string
		var count int
		if err = rows.Scan(&phase, &count); err != nil {
			rows.Close()
			return page, err
		}
		page.Counts[phase] = count
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return page, err
	}
	rows.Close()
	for phase, count := range page.Counts {
		if filter.Phase == "" || phase == filter.Phase {
			page.Total += count
		}
	}
	if filter.Phase != "" {
		where += " AND (" + phaseSQL + ")=?"
		args = append(args, today, filter.Phase)
	}
	args = append(args, filter.Limit, filter.Offset)
	rows, err = tx.QueryContext(ctx, "SELECT "+wantColumns+" FROM catalog_wants w WHERE "+where+" ORDER BY w.created_at DESC,w.id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		w, err := scanWant(rows, today)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, w)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return page, err
	}
	rows.Close()
	return page, tx.Commit()
}

// Linking a downloader is only provenance, never evidence of acquiring a file.
// The caller owns the same transaction which reserves the durable producer.
func LinkWantJobTx(ctx context.Context, tx *sql.Tx, code string, jobID int64) error {
	if jobID < 1 {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, "UPDATE catalog_wants SET last_job_id=?,version=version+1,updated_at=? WHERE code=? AND state<>'acquired' AND last_job_id<>?", jobID, timestamp(), code, jobID)
	return err
}

// ReconcileWantsTx accepts exact assets already verified and published by the
// caller. It shares scan/registration/producer commit or rolls back with them.
func ReconcileWantsTx(ctx context.Context, tx *sql.Tx, assetIDs []string, source string, producerID int64, stamp string) error {
	if len(assetIDs) == 0 {
		return nil
	}
	if producerID < 0 || (source != "scan" && source != "registration" && source != "organizer") {
		return ErrInvalid
	}
	for start := 0; start < len(assetIDs); start += 200 {
		end := min(start+200, len(assetIDs))
		args := make([]any, 0, end-start)
		for _, id := range assetIDs[start:end] {
			args = append(args, id)
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.work_id,w.code,w.title,w.release_date,w.studio,w.actors FROM catalog_assets a JOIN catalog_works w ON w.id=a.work_id WHERE a.id IN (`+marks+`) AND a.available=1 AND w.code<>'' ORDER BY a.id`, args...)
		if err != nil {
			return err
		}
		type item struct{ asset, work, code, title, released, studio, actors string }
		items := []item{}
		for rows.Next() {
			var i item
			if err = rows.Scan(&i.asset, &i.work, &i.code, &i.title, &i.released, &i.studio, &i.actors); err != nil {
				rows.Close()
				return err
			}
			items = append(items, i)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, i := range items {
			proof := WantAcquisition{AssetID: i.asset, WorkID: i.work, Source: source, Batch: source + ":" + stamp, AcquiredAt: stamp, ProducerJobID: producerID}
			_, err = tx.ExecContext(ctx, `UPDATE catalog_wants SET state='acquired',acquisition=?,version=version+1,updated_at=?,
 last_job_id=CASE WHEN ?>0 THEN ? ELSE last_job_id END,
 title=CASE WHEN title='' THEN ? ELSE title END,release_date=CASE WHEN release_date='' THEN ? ELSE release_date END,
 studio=CASE WHEN studio='' THEN ? ELSE studio END,actors=CASE WHEN json_array_length(actors)=0 THEN ? ELSE actors END
 WHERE code=? AND state<>'acquired'`, jsonText(proof), stamp, producerID, producerID, i.title, i.released, i.studio, i.actors, i.code)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func acquiredAssetIDs(assets []discoveredAsset) []string {
	ids := make([]string, 0, len(assets))
	for _, asset := range assets {
		ids = append(ids, asset.ID)
	}
	return ids
}
