package catalog

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Deletion never accepts filesystem paths from the client. The preview is
// rebuilt under the scan lock and compared before any irreversible operation.
type DeleteFile struct {
	LibraryID    int64  `json:"library_id"`
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Size         int64  `json:"size"`
	root         string
	rootIdentity string
	identity     string
	parents      string
}
type DeletePlan struct {
	WorkIDs  []string     `json:"work_ids"`
	Videos   int          `json:"videos"`
	Files    []DeleteFile `json:"files"`
	Retained []string     `json:"retained"`
	Missing  []string     `json:"missing"`
	Token    string       `json:"token"`
}
type DeleteResult struct {
	DeletedFiles int      `json:"deleted_files"`
	DeletedWorks int      `json:"deleted_works"`
	Failed       []string `json:"failed"`
}
type deleteAsset struct {
	id, work, relative, nfo, poster, root, rootIdentity, fingerprint string
	libraryID                                                        int64
	available                                                        bool
	subtitles                                                        []Subtitle
}

func normalizedWorkIDs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, ErrInvalid
	}
	result := append([]string(nil), ids...)
	sort.Strings(result)
	for i, id := range result {
		if len(id) < 1 || len(id) > 100 || (i > 0 && id == result[i-1]) {
			return nil, ErrInvalid
		}
	}
	return result, nil
}

func (s *Store) deletionAssets(ctx context.Context) ([]deleteAsset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.work_id,a.library_id,a.path,a.nfo_path,a.poster_path,a.available,a.file_identity,a.subtitles,l.root,l.root_identity FROM catalog_assets a JOIN catalog_libraries l ON l.id=a.library_id ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []deleteAsset{}
	for rows.Next() {
		var a deleteAsset
		var subs string
		if err := rows.Scan(&a.id, &a.work, &a.libraryID, &a.relative, &a.nfo, &a.poster, &a.available, &a.fingerprint, &subs, &a.root, &a.rootIdentity); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(subs), &a.subtitles); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func physicalFile(root, relative string) string {
	return filepath.Join(root, filepath.FromSlash(relative))
}

func deletionFile(a deleteAsset, relative, kind string) (DeleteFile, error) {
	result := DeleteFile{LibraryID: a.libraryID, Path: relative, Kind: kind, root: a.root, rootIdentity: a.rootIdentity}
	if !fs.ValidPath(relative) || relative == "." {
		return result, ErrInvalid
	}
	root, err := openLibraryRoot(a.root, a.rootIdentity)
	if err != nil {
		return result, err
	}
	defer root.Close()
	// Do not follow even an in-root symlink for destructive operations.
	parts := strings.Split(relative, "/")
	parentIDs := []string{}
	for i := 1; i < len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i], "/"))
		if err != nil {
			return result, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, ErrConflict
		}
		id, err := directoryIdentity(info)
		if err != nil {
			return result, err
		}
		parentIDs = append(parentIDs, id)
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, ErrConflict
	}
	identity, err := directoryIdentity(info)
	if err != nil {
		return result, err
	}
	result.Size = info.Size()
	result.identity = stableID(identity, strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10))
	result.parents = strings.Join(parentIDs, "/")
	if kind == "video" && (a.fingerprint == "" || a.fingerprint != result.identity) {
		return result, ErrConflict
	}
	return result, nil
}

func (s *Store) previewDelete(ctx context.Context, ids []string) (DeletePlan, error) {
	plan := DeletePlan{WorkIDs: ids, Files: []DeleteFile{}, Retained: []string{}, Missing: []string{}}
	selected := map[string]bool{}
	for _, id := range ids {
		var present int
		if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM catalog_works WHERE id=?", id).Scan(&present); errors.Is(err, sql.ErrNoRows) {
			return plan, ErrNotFound
		} else if err != nil {
			return plan, err
		}
		selected[id] = true
	}
	assets, err := s.deletionAssets(ctx)
	if err != nil {
		return plan, err
	}
	protected := map[string]bool{}
	videos := map[string]bool{}
	generations := []string{}
	checkedParents := map[string]bool{}
	for _, a := range assets {
		if selected[a.work] {
			generations = append(generations, a.id+":"+a.fingerprint)
			parent := physicalFile(a.root, path.Dir(a.relative))
			if !checkedParents[parent] {
				root, err := openLibraryRoot(a.root, a.rootIdentity)
				if err != nil {
					return plan, err
				}
				entries, readErr := fs.ReadDir(root.FS(), path.Dir(a.relative))
				root.Close()
				if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
					return plan, readErr
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".catalog-delete-") {
						return plan, fmt.Errorf("%w: %s", ErrDeletePending, filepath.Join(parent, entry.Name()))
					}
				}
				checkedParents[parent] = true
			}
		}
		if !a.available {
			continue
		}
		if selected[a.work] {
			videos[physicalFile(a.root, a.relative)] = true
			continue
		}
		for _, p := range []string{a.relative, a.nfo, a.poster} {
			if p != "" {
				protected[physicalFile(a.root, p)] = true
			}
		}
		for _, sub := range a.subtitles {
			protected[physicalFile(a.root, sub.Path)] = true
		}
	}
	files := map[string]DeleteFile{}
	retained := map[string]bool{}
	missing := map[string]bool{}
	add := func(a deleteAsset, relative, kind string) error {
		if relative == "" {
			return nil
		}
		absolute := physicalFile(a.root, relative)
		if protected[absolute] {
			if kind == "video" {
				return ErrConflict
			}
			retained[absolute] = true
			return nil
		}
		f, err := deletionFile(a, relative, kind)
		if errors.Is(err, fs.ErrNotExist) {
			missing[absolute] = true
			return nil
		}
		if err != nil {
			return err
		}
		if old, ok := files[absolute]; !ok || old.Kind != "video" {
			files[absolute] = f
		}
		return nil
	}
	for _, a := range assets {
		if !selected[a.work] || !a.available {
			continue
		}
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		if err := add(a, a.relative, "video"); err != nil {
			return plan, err
		}
		root, err := openLibraryRoot(a.root, a.rootIdentity)
		if err != nil {
			return plan, err
		}
		entries, err := fs.ReadDir(root.FS(), path.Dir(a.relative))
		if err != nil {
			root.Close()
			return plan, err
		}
		directory, dirErr := sidecarDirectory(root, path.Dir(a.relative))
		if dirErr != nil {
			root.Close()
			return plan, dirErr
		}
		for _, video := range directory.videos {
			if videos[physicalFile(a.root, path.Join(path.Dir(a.relative), video))] {
				continue
			}
			code := s.code(strings.TrimSuffix(video, path.Ext(video)))
			nfo := s.nfoSidecar(directory, video, code)
			if nfo != "" {
				protected[physicalFile(a.root, nfo)] = true
				if raw, readErr := readBounded(root, nfo, maxNFOBytes); readErr == nil {
					if meta, parseErr := s.parseNFO(raw); parseErr == nil {
						if poster := safeArtwork(path.Dir(nfo), meta.Poster); poster != "" {
							protected[physicalFile(a.root, poster)] = true
						}
					}
				}
			}
		}
		root.Close()
		others := false
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".catalog-delete-") {
				return plan, fmt.Errorf("%w: %s", ErrDeletePending, physicalFile(a.root, path.Join(path.Dir(a.relative), entry.Name())))
			}
			rel := path.Join(path.Dir(a.relative), entry.Name())
			if videoExtensions[strings.ToLower(path.Ext(rel))] != "" && !videos[physicalFile(a.root, rel)] {
				others = true
			}
		}
		stem := strings.TrimSuffix(path.Base(a.relative), path.Ext(a.relative))
		candidates := map[string]string{a.nfo: "nfo", a.poster: "image"}
		for _, sub := range a.subtitles {
			candidates[sub.Path] = "subtitle"
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := strings.ToLower(path.Ext(name))
			base := strings.TrimSuffix(name, path.Ext(name))
			kind := ""
			switch ext {
			case ".nfo":
				kind = "nfo"
			case ".jpg", ".jpeg", ".png", ".webp":
				kind = "image"
			case ".srt", ".vtt", ".ass", ".ssa":
				kind = "subtitle"
			}
			if kind == "" {
				continue
			}
			if base == stem || base == stem+"-poster" || base == stem+"-fanart" || base == stem+"-thumb" || (kind == "subtitle" && strings.HasPrefix(base, stem+".")) {
				candidates[path.Join(path.Dir(a.relative), name)] = kind
			}
		}
		for relative, kind := range candidates {
			if relative == "" {
				continue
			}
			base := strings.TrimSuffix(path.Base(relative), path.Ext(relative))
			exclusive := path.Dir(relative) == path.Dir(a.relative) && (base == stem || base == stem+"-poster" || base == stem+"-fanart" || base == stem+"-thumb" || (kind == "subtitle" && strings.HasPrefix(base, stem+".")))
			if others && !exclusive {
				retained[physicalFile(a.root, relative)] = true
				continue
			}
			if err := add(a, relative, kind); err != nil {
				return plan, err
			}
		}
		if len(files) > 10000 {
			return plan, ErrInvalid
		}
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		if protected[k] {
			if files[k].Kind == "video" {
				return plan, ErrConflict
			}
			retained[k] = true
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	signature := []string{strings.Join(ids, ","), strings.Join(generations, ",")}
	for _, k := range keys {
		f := files[k]
		plan.Files = append(plan.Files, f)
		if f.Kind == "video" {
			plan.Videos++
		}
		signature = append(signature, k, f.Kind, f.rootIdentity, f.identity, f.parents)
	}
	for p := range retained {
		plan.Retained = append(plan.Retained, p)
	}
	sort.Strings(plan.Retained)
	for p := range missing {
		plan.Missing = append(plan.Missing, p)
	}
	sort.Strings(plan.Missing)
	signature = append(signature, strings.Join(plan.Retained, "\n"), strings.Join(plan.Missing, "\n"))
	plan.Token = stableID(signature...)
	return plan, nil
}

func (s *Store) PreviewDelete(ctx context.Context, workIDs []string) (DeletePlan, error) {
	ids, err := normalizedWorkIDs(workIDs)
	if err != nil {
		return DeletePlan{}, err
	}
	if !s.scanMu.TryLock() {
		return DeletePlan{}, ErrBusy
	}
	defer s.scanMu.Unlock()
	return s.previewDelete(ctx, ids)
}

func (s *Store) Delete(ctx context.Context, workIDs []string, token string) (DeleteResult, error) {
	result := DeleteResult{Failed: []string{}}
	ids, err := normalizedWorkIDs(workIDs)
	if err != nil || len(token) != len(stableID("")) {
		return result, ErrInvalid
	}
	if !s.scanMu.TryLock() {
		return result, ErrBusy
	}
	defer s.scanMu.Unlock()
	plan, err := s.previewDelete(ctx, ids)
	if err != nil {
		return result, err
	}
	if subtle.ConstantTimeCompare([]byte(plan.Token), []byte(token)) != 1 {
		return result, ErrConflict
	}
	// Filesystem changes cannot be rolled back by SQLite. On partial failure keep
	// catalog rows, report the exact remaining paths, and require a new preview.
	for _, f := range plan.Files {
		if err := ctx.Err(); err != nil {
			result.Failed = append(result.Failed, "请求中断，剩余文件未删除")
			return result, nil
		}
		a := deleteAsset{root: f.root, rootIdentity: f.rootIdentity, fingerprint: f.identity, libraryID: f.LibraryID}
		current, err := deletionFile(a, f.Path, f.Kind)
		if err == nil && (current.identity != f.identity || current.parents != f.parents) {
			err = ErrConflict
		}
		if err == nil {
			var root *os.Root
			root, err = openLibraryRoot(f.root, f.rootIdentity)
			if err == nil {
				var parent *os.Root
				parent, err = root.OpenRoot(path.Dir(f.Path))
				if err == nil {
					var info fs.FileInfo
					info, err = parent.Stat(".")
					if err == nil {
						var identity string
						identity, err = directoryIdentity(info)
						expected := f.rootIdentity
						if f.parents != "" {
							parts := strings.Split(f.parents, "/")
							expected = parts[len(parts)-1]
						}
						if err == nil && identity != expected {
							err = ErrConflict
						}
					}
					if err == nil {
						err = removeCapturedFile(parent, path.Base(f.Path), f.identity)
					}
					parent.Close()
				}
				root.Close()
			}
		}
		if err != nil {
			result.Failed = append(result.Failed, f.Path+"："+err.Error())
			return result, nil
		}
		result.DeletedFiles++
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("files deleted, catalog cleanup: %w", err)
	}
	defer tx.Rollback()
	for _, id := range ids {
		for _, query := range []string{"DELETE FROM catalog_progress WHERE asset_id IN (SELECT id FROM catalog_assets WHERE work_id=?)", "DELETE FROM catalog_assets WHERE work_id=?", "DELETE FROM catalog_personal WHERE work_id=?", "DELETE FROM catalog_works WHERE id=?"} {
			if _, err := tx.ExecContext(ctx, query, id); err != nil {
				return result, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.DeletedWorks = len(ids)
	return result, nil
}
