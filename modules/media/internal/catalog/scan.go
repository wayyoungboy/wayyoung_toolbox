package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func openLibraryRoot(value string, expected ...string) (*os.Root, error) {
	info, err := os.Lstat(value)
	if err != nil {
		return nil, ErrUnavailable
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrConflict
	}
	identity, err := directoryIdentity(info)
	if err != nil {
		return nil, err
	}
	if len(expected) > 0 && (expected[0] == "" || expected[0] != identity) {
		return nil, ErrConflict
	}
	root, err := os.OpenRoot(value)
	if err != nil {
		return nil, ErrUnavailable
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, ErrConflict
	}
	return root, nil
}

func validateLibraryRoot(rootPath string) (Library, error) {
	rootPath = strings.TrimSpace(rootPath)
	if !filepath.IsAbs(rootPath) || len(rootPath) > 4096 || strings.ContainsRune(rootPath, 0) {
		return Library{}, ErrInvalid
	}
	for _, component := range strings.Split(filepath.ToSlash(rootPath), "/") {
		if component == ".." {
			return Library{}, ErrInvalid
		}
	}
	if filepath.Clean(rootPath) == string(filepath.Separator) {
		return Library{}, ErrInvalid
	}
	info, err := os.Lstat(rootPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Library{}, ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return Library{}, ErrInvalid
	}
	root, err := openLibraryRoot(resolved)
	if err != nil {
		return Library{}, ErrInvalid
	}
	opened, statErr := root.Stat(".")
	if statErr != nil {
		root.Close()
		return Library{}, statErr
	}
	identity, identityErr := directoryIdentity(opened)
	if identityErr != nil {
		root.Close()
		return Library{}, identityErr
	}
	if err := root.Close(); err != nil {
		return Library{}, err
	}
	return Library{Root: resolved, RootIdentity: identity}, nil
}

func (s *Store) AddLibrary(ctx context.Context, name, rootPath string) (Library, error) {
	if !s.scanMu.TryLock() {
		return Library{}, ErrBusy
	}
	defer s.scanMu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 160 || !utf8.ValidString(name) {
		return Library{}, ErrInvalid
	}
	location, err := validateLibraryRoot(rootPath)
	if err != nil {
		return Library{}, err
	}
	var existing int64
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM catalog_libraries WHERE root=?", location.Root).Scan(&existing); err == nil {
		return Library{}, ErrConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Library{}, err
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO catalog_libraries(name,root,root_identity,created_at) VALUES(?,?,?,?)", name, location.Root, location.RootIdentity, timestamp())
	if err != nil {
		return Library{}, fmt.Errorf("save library: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Library{}, err
	}
	return s.library(ctx, id)
}

// RelinkLibrary is an explicit administrator action. Existing identities and
// progress remain, but files cannot be served until the new root is scanned.
func (s *Store) RelinkLibrary(ctx context.Context, id int64, rootPath string) (Library, error) {
	if !s.scanMu.TryLock() {
		return Library{}, ErrBusy
	}
	defer s.scanMu.Unlock()
	if _, err := s.library(ctx, id); err != nil {
		return Library{}, err
	}
	location, err := validateLibraryRoot(rootPath)
	if err != nil {
		return Library{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	var others int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_libraries WHERE root=? AND id!=?", location.Root, id).Scan(&others); err != nil {
		return Library{}, err
	}
	if others != 0 {
		return Library{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_libraries SET root=?,root_identity=?,scanned_at='',scan_error=? WHERE id=?", location.Root, location.RootIdentity, "目录已重新关联，请扫描馆藏后播放", id); err != nil {
		return Library{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_assets SET available=0 WHERE library_id=?", id); err != nil {
		return Library{}, err
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	return s.library(ctx, id)
}

type discoveredAsset struct {
	ID           string
	WorkID       string
	Path         string
	Size         int64
	ModifiedNS   int64
	FileIdentity string
	NFOPath      string
	PosterPath   string
	Issues       []string
	Subtitles    []Subtitle
	Metadata     metadata
}

func skippedDirectory(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, ".") || name == "#recycle" || name == "#snapshot" || name == "@recycle" || name == "@eadir" || name == "$recycle.bin"
}

func (s *Store) discover(ctx context.Context, root *os.Root, libraryID int64) ([]discoveredAsset, error) {
	assets := []discoveredAsset{}
	directories := map[string]directorySidecars{}
	visited := 0
	err := fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > 200000 {
			return fmt.Errorf("%w：单次扫描超过目录条目上限", ErrInvalid)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if relative != "." && skippedDirectory(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if videoExtensions[strings.ToLower(path.Ext(relative))] == "" {
			return nil
		}
		info, err := root.Lstat(relative)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil
		}
		directory, ok := directories[path.Dir(relative)]
		if !ok {
			directory, err = sidecarDirectory(root, path.Dir(relative))
			if err != nil {
				return err
			}
			directories[path.Dir(relative)] = directory
		}
		item, err := s.inspectAsset(root, libraryID, relative, info, directory)
		if err != nil {
			return err
		}
		assets = append(assets, item)
		return nil
	})
	return assets, err
}

// Scan publishes a full, successful inventory in one transaction. A failed or
// canceled traversal never marks previously indexed assets missing.
func (s *Store) Scan(ctx context.Context, id int64) (result ScanResult, resultErr error) {
	if !s.scanMu.TryLock() {
		return result, ErrBusy
	}
	defer s.scanMu.Unlock()
	library, err := s.library(ctx, id)
	if err != nil {
		return result, err
	}
	defer func() {
		if resultErr != nil {
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_, _ = s.db.ExecContext(failureCtx, "UPDATE catalog_libraries SET scan_error=? WHERE id=?", "扫描未完成，保留上次馆藏记录", id)
		}
	}()
	root, err := openLibraryRoot(library.Root, library.RootIdentity)
	if err != nil {
		return result, err
	}
	defer root.Close()
	assets, err := s.discover(ctx, root, id)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	stamp, token := timestamp(), rand.Text()
	transfers := map[string]map[string]bool{}
	workCount, err := s.publishAssets(ctx, tx, map[int64][]discoveredAsset{library.ID: assets}, stamp, token, transfers)
	if err != nil {
		return result, err
	}
	for oldID, destinations := range transfers {
		if err := transferOrphanedPersonal(ctx, tx, oldID, destinations); err != nil {
			return result, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_assets SET available=0 WHERE library_id=? AND scan_token!=?", library.ID, token); err != nil {
		return result, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_assets WHERE library_id=? AND scan_token=? AND json_array_length(issues)>0`, library.ID, token).Scan(&result.Issues); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_libraries SET scanned_at=?,scan_error='' WHERE id=?", stamp, library.ID); err != nil {
		return result, err
	}
	if err := syncActorsTx(ctx, tx); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.LibraryID, result.ScannedAt, result.Assets, result.Works = library.ID, stamp, len(assets), workCount
	return result, nil
}

// An identity correction changes the work, not the user's intent. Copy personal
// state only when no asset remains attached to the old identity; never discard
// a conflicting manual destination title or its original source record.
func transferOrphanedPersonal(ctx context.Context, tx *sql.Tx, oldID string, destinations map[string]bool) error {
	var remaining int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_assets WHERE work_id=?", oldID).Scan(&remaining); err != nil {
		return err
	}
	if remaining != 0 {
		return nil
	}
	if len(destinations) != 1 {
		// A split identity has no unambiguous destination for a rating or a
		// list position. Keep the original intent for explicit resolution.
		for newID := range destinations {
			if _, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET issues=json_insert(issues,'$[#]','personal_identity_conflict') WHERE work_id=?`, newID); err != nil {
				return err
			}
		}
		return nil
	}
	keepSourceNotes := false
	for newID := range destinations {
		var conflict bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_work_notes o,catalog_work_notes n WHERE o.work_id=? AND n.work_id=? AND
 ((o.watched IS NOT NULL AND n.watched IS NOT NULL AND o.watched!=n.watched) OR
 (o.rating!=0 AND n.rating!=0 AND o.rating!=n.rating) OR
 (o.feedback!='' AND n.feedback!='' AND o.feedback!=n.feedback)))`, oldID, newID).Scan(&conflict); err != nil {
			return err
		}
		if conflict {
			keepSourceNotes = true
			if _, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET issues=json_insert(issues,'$[#]','personal_notes_conflict') WHERE work_id=?`, newID); err != nil {
				return err
			}
		}
		// Empty destination fields are not an intentional replacement of source
		// notes. Merge fields independently, preserving explicitly set values.
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_work_notes(work_id,watched,rating,feedback,revision)
 SELECT ?,watched,rating,feedback,revision FROM catalog_work_notes WHERE work_id=?
 ON CONFLICT(work_id) DO UPDATE SET
 watched=COALESCE(catalog_work_notes.watched,excluded.watched),
 rating=CASE WHEN catalog_work_notes.rating=0 THEN excluded.rating ELSE catalog_work_notes.rating END,
 feedback=CASE WHEN catalog_work_notes.feedback='' THEN excluded.feedback ELSE catalog_work_notes.feedback END,
 revision=catalog_work_notes.revision+1`, newID, oldID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT list_id,position FROM catalog_list_items WHERE work_id=?", oldID)
		if err != nil {
			return err
		}
		type membership struct {
			list     string
			position int
		}
		members := []membership{}
		for rows.Next() {
			var item membership
			if err := rows.Scan(&item.list, &item.position); err != nil {
				rows.Close()
				return err
			}
			members = append(members, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range members {
			var already int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_list_items WHERE list_id=? AND work_id=?", item.list, newID).Scan(&already); err != nil {
				return err
			}
			if already == 0 {
				if _, err := tx.ExecContext(ctx, "UPDATE catalog_list_items SET work_id=? WHERE list_id=? AND work_id=?", newID, item.list, oldID); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_list_items WHERE list_id=? AND work_id=?", item.list, oldID); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, "UPDATE catalog_lists SET version=version+1,updated_at=? WHERE id=?", timestamp(), item.list); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_personal(work_id,favorite,watch_later)
SELECT ?,favorite,watch_later FROM catalog_personal WHERE work_id=?
ON CONFLICT(work_id) DO UPDATE SET favorite=MAX(favorite,excluded.favorite),watch_later=MAX(watch_later,excluded.watch_later)`, newID, oldID); err != nil {
			return err
		}
		var oldTitle, newTitle string
		var oldLocked, newLocked bool
		if err := tx.QueryRowContext(ctx, `SELECT o.title,o.title_locked,n.title,n.title_locked FROM catalog_works o,catalog_works n WHERE o.id=? AND n.id=?`, oldID, newID).Scan(&oldTitle, &oldLocked, &newTitle, &newLocked); err != nil {
			return err
		}
		if oldLocked && !newLocked {
			if _, err := tx.ExecContext(ctx, "UPDATE catalog_works SET title=?,title_locked=1 WHERE id=?", oldTitle, newID); err != nil {
				return err
			}
		} else if oldLocked && newLocked && oldTitle != newTitle {
			if _, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET issues=json_insert(issues,'$[#]','manual_title_conflict') WHERE work_id=?`, newID); err != nil {
				return err
			}
		}
	}
	// This is a move of current intent, not a second live personal-state copy.
	// Otherwise a later return to the old identity can resurrect canceled flags.
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_personal WHERE work_id=?", oldID); err != nil {
		return err
	}
	if !keepSourceNotes {
		if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_work_notes WHERE work_id=?", oldID); err != nil {
			return err
		}
	}
	return nil
}

// inspectAsset is shared by full inventories and receipt-bound targeted updates.
func (s *Store) inspectAsset(root *os.Root, libraryID int64, relative string, info fs.FileInfo, directory directorySidecars) (discoveredAsset, error) {
	identity, err := directoryIdentity(info)
	if err != nil {
		return discoveredAsset{}, err
	}
	fingerprint := stableID(identity, strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10))
	stem := strings.TrimSuffix(path.Base(relative), path.Ext(path.Base(relative)))
	code := s.code(stem)
	item := discoveredAsset{ID: stableID("asset-v2", strconv.FormatInt(libraryID, 10), relative, fingerprint), FileIdentity: fingerprint, Path: relative,
		Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Issues: []string{},
		Metadata: metadata{Code: code, Title: stem, Actors: []string{}, Genres: []string{}, Source: "filename"}}
	item.NFOPath = s.nfoSidecar(directory, path.Base(relative), code)
	if item.NFOPath != "" {
		raw, readErr := readBounded(root, item.NFOPath, maxNFOBytes)
		if readErr != nil {
			item.Issues = append(item.Issues, "nfo_unreadable")
		} else if imported, parseErr := s.parseNFO(raw); parseErr != nil {
			item.Issues = append(item.Issues, "nfo_invalid")
		} else if code != "" && imported.Code != "" && code != imported.Code {
			item.Issues = append(item.Issues, "identity_conflict")
		} else {
			if imported.Code == "" {
				imported.Code = code
			}
			if imported.Title == "" {
				imported.Title = stem
				item.Issues = append(item.Issues, "title_missing")
			}
			item.Metadata = imported
		}
	} else {
		item.Issues = append(item.Issues, "nfo_missing")
	}
	if item.Metadata.Code == "" {
		item.Issues = append(item.Issues, "identity_unknown")
	}
	if item.Metadata.Code != "" {
		item.WorkID = stableID("work", item.Metadata.Code)
	} else {
		item.WorkID = stableID("unknown", item.ID)
	}
	item.PosterPath = s.posterSidecar(root, directory, relative, item.Metadata.Code, item.Metadata.Poster)
	item.Subtitles = subtitleSidecars(directory, relative)
	return item, nil
}

// publishAssets only upserts its explicit batch; missing-file reconciliation is
// deliberately owned by the successful full-inventory caller.
func (s *Store) publishAssets(ctx context.Context, tx *sql.Tx, batches map[int64][]discoveredAsset, stamp, token string, transfers map[string]map[string]bool) (int, error) {
	works := map[string]metadata{}
	libraryIDs := make([]int64, 0, len(batches))
	for libraryID := range batches {
		libraryIDs = append(libraryIDs, libraryID)
	}
	sort.Slice(libraryIDs, func(i, j int) bool { return libraryIDs[i] < libraryIDs[j] })
	for _, libraryID := range libraryIDs {
		assets := batches[libraryID]
		sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
		for index := range assets {
			asset := &assets[index]
			// Preserve IDs from the pre-fingerprint schema only when its recorded
			// size/mtime match. Subsequent replacements always get a new generation.
			var legacyID string
			legacyErr := tx.QueryRowContext(ctx, `SELECT id FROM catalog_assets WHERE library_id=? AND path=? AND file_identity='' AND size=? AND modified_ns=? ORDER BY id LIMIT 1`, libraryID, asset.Path, asset.Size, asset.ModifiedNS).Scan(&legacyID)
			if legacyErr != nil && !errors.Is(legacyErr, sql.ErrNoRows) {
				return 0, legacyErr
			}
			if legacyID != "" {
				asset.ID = legacyID
				if asset.Metadata.Code == "" {
					asset.WorkID = stableID("unknown", asset.ID)
				}
			} else {
				// Migrated IDs remain stable after their first fingerprinted scan.
				var existingID string
				existingErr := tx.QueryRowContext(ctx, `SELECT id FROM catalog_assets WHERE library_id=? AND path=? AND file_identity=? ORDER BY id LIMIT 1`, libraryID, asset.Path, asset.FileIdentity).Scan(&existingID)
				if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
					return 0, existingErr
				}
				if existingID != "" {
					asset.ID = existingID
					if asset.Metadata.Code == "" {
						asset.WorkID = stableID("unknown", asset.ID)
					}
				}
			}
			if asset.Metadata.Code == "" {
				var oldID, oldCode string
				var oldSize, oldModified int64
				oldErr := tx.QueryRowContext(ctx, `SELECT a.work_id,w.code,a.size,a.modified_ns FROM catalog_assets a JOIN catalog_works w ON w.id=a.work_id WHERE a.id=?`, asset.ID).Scan(&oldID, &oldCode, &oldSize, &oldModified)
				if oldErr != nil && !errors.Is(oldErr, sql.ErrNoRows) {
					return 0, oldErr
				}
				// Losing a sidecar is not evidence that the unchanged file became a
				// different work. Keep the known identity and all richer stored fields.
				if oldCode != "" && oldSize == asset.Size && oldModified == asset.ModifiedNS {
					asset.WorkID, asset.Metadata.Code = oldID, oldCode
					retained := []string{"identity_retained"}
					for _, issue := range asset.Issues {
						if issue != "identity_unknown" {
							retained = append(retained, issue)
						}
					}
					asset.Issues = retained
				}
			}
			previous, exists := works[asset.WorkID]
			if exists && previous.TitleProvided && asset.Metadata.TitleProvided && previous.Title != asset.Metadata.Title {
				asset.Issues = append(asset.Issues, "metadata_conflict")
			}
			if !exists {
				works[asset.WorkID] = asset.Metadata
			} else {
				works[asset.WorkID] = mergeMetadata(previous, asset.Metadata)
			}
		}
	}
	for id, data := range works {
		rank := 0
		if data.Source == "local_nfo" {
			rank = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_works(id,code,title,original_title,plot,studio,release_date,year,actors,genres,metadata_source,source_rank,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
title=CASE WHEN catalog_works.title_locked=0 AND ? AND excluded.source_rank>=catalog_works.source_rank THEN excluded.title ELSE catalog_works.title END,
original_title=CASE WHEN excluded.original_title!='' AND excluded.source_rank>=catalog_works.source_rank THEN excluded.original_title ELSE catalog_works.original_title END,
plot=CASE WHEN excluded.plot!='' AND excluded.source_rank>=catalog_works.source_rank THEN excluded.plot ELSE catalog_works.plot END,
studio=CASE WHEN excluded.studio!='' AND excluded.source_rank>=catalog_works.source_rank THEN excluded.studio ELSE catalog_works.studio END,
release_date=CASE WHEN excluded.release_date!='' AND excluded.source_rank>=catalog_works.source_rank THEN excluded.release_date ELSE catalog_works.release_date END,
year=CASE WHEN excluded.year!=0 AND excluded.source_rank>=catalog_works.source_rank THEN excluded.year ELSE catalog_works.year END,
actors=CASE WHEN json_array_length(excluded.actors)>0 AND excluded.source_rank>=catalog_works.source_rank THEN excluded.actors ELSE catalog_works.actors END,
genres=CASE WHEN json_array_length(excluded.genres)>0 AND excluded.source_rank>=catalog_works.source_rank THEN excluded.genres ELSE catalog_works.genres END,
metadata_source=CASE WHEN excluded.source_rank>=catalog_works.source_rank THEN excluded.metadata_source ELSE catalog_works.metadata_source END,
source_rank=MAX(catalog_works.source_rank,excluded.source_rank),updated_at=excluded.updated_at`,
			id, data.Code, data.Title, data.OriginalTitle, data.Plot, data.Studio, data.ReleaseDate, data.Year,
			jsonText(data.Actors), jsonText(data.Genres), data.Source, rank, stamp, stamp, data.TitleProvided)
		if err != nil {
			return 0, err
		}
	}
	for _, libraryID := range libraryIDs {
		assets := batches[libraryID]
		for _, asset := range assets {
			var oldID string
			lookupErr := tx.QueryRowContext(ctx, "SELECT work_id FROM catalog_assets WHERE id=?", asset.ID).Scan(&oldID)
			if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
				return 0, lookupErr
			}
			if oldID != "" && oldID != asset.WorkID {
				if transfers[oldID] == nil {
					transfers[oldID] = map[string]bool{}
				}
				transfers[oldID][asset.WorkID] = true
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO catalog_assets(id,library_id,work_id,path,size,modified_ns,file_identity,available,nfo_path,poster_path,issues,subtitles,metadata,scan_token)
	VALUES(?,?,?,?,?,?,?,1,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET library_id=excluded.library_id,path=excluded.path,work_id=excluded.work_id,size=excluded.size,
	modified_ns=excluded.modified_ns,file_identity=excluded.file_identity,available=1,nfo_path=excluded.nfo_path,poster_path=excluded.poster_path,
	issues=excluded.issues,subtitles=excluded.subtitles,metadata=excluded.metadata,scan_token=excluded.scan_token`,
				asset.ID, libraryID, asset.WorkID, asset.Path, asset.Size, asset.ModifiedNS, asset.FileIdentity, asset.NFOPath, asset.PosterPath,
				jsonText(asset.Issues), jsonText(asset.Subtitles), jsonText(asset.Metadata), token)
			if err != nil {
				return 0, err
			}
		}
	}
	return len(works), nil
}
