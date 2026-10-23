package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RegistrationTarget is an exact output bound to the organizer's durable file
// receipt. PreviousPath is supplied only for a proven move, never for a copy or
// hard link; it permits keeping an indexed asset's progress across relocation.
type RegistrationTarget struct {
	Path         string
	Identity     string
	Size         int64
	ModifiedNS   int64
	PreviousPath string
}

type RegistrationResult struct {
	Assets     int      `json:"asset_count"`
	Works      int      `json:"work_count"`
	LibraryIDs []int64  `json:"library_ids"`
	WorkIDs    []string `json:"work_ids"`
}

func registrationAbsolute(value string) bool {
	if !utf8.ValidString(value) || len(value) > 4096 || strings.ContainsRune(value, 0) || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	return value != string(filepath.Separator)
}

func registrationRelative(libraries []Library, filename string) (Library, string, bool) {
	// The query is ordered by root length, so an unavailable explicitly
	// registered nested root can never silently fall back to its ancestor.
	for _, library := range libraries {
		relative, err := filepath.Rel(library.Root, filename)
		if err == nil && relative != "." && filepath.IsLocal(relative) {
			return library, filepath.ToSlash(relative), true
		}
	}
	return Library{}, "", false
}

func (s *Store) registrationLibraries(ctx context.Context) ([]Library, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,root,root_identity FROM catalog_libraries ORDER BY length(root) DESC,id LIMIT 1025")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Library{}
	for rows.Next() {
		var item Library
		if err := rows.Scan(&item.ID, &item.Root, &item.RootIdentity); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) > 1024 {
		return nil, ErrInvalid
	}
	return items, rows.Err()
}

func registrationInfo(root *os.Root, relative string, target RegistrationTarget) (os.FileInfo, error) {
	if !fs.ValidPath(relative) || relative == "." {
		return nil, ErrInvalid
	}
	parts := strings.Split(relative, "/")
	for i := 1; i < len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i], "/"))
		if err != nil {
			return nil, ErrUnavailable
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrConflict
		}
	}
	file, info, err := openRegular(root, relative)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	identity, err := directoryIdentity(info)
	if err != nil || identity != target.Identity || info.Size() != target.Size || info.ModTime().UnixNano() != target.ModifiedNS {
		return nil, ErrConflict
	}
	return info, nil
}

// RegisterFiles publishes only the supplied, receipt-verified outputs. It never
// walks a library, reconciles absent files, or claims a full scan completed.
// finish shares the publication transaction so a canceled/stale producer can
// reject the entire batch before any catalog or completion state is committed.
func (s *Store) RegisterFiles(ctx context.Context, targets []RegistrationTarget, finish func(*sql.Tx, RegistrationResult) error) (RegistrationResult, error) {
	result := RegistrationResult{LibraryIDs: []int64{}, WorkIDs: []string{}}
	if len(targets) == 0 || len(targets) > 1000 {
		return result, ErrInvalid
	}
	if !s.scanMu.TryLock() {
		return result, ErrBusy
	}
	defer s.scanMu.Unlock()
	libraries, err := s.registrationLibraries(ctx)
	if err != nil {
		return result, err
	}
	roots := map[int64]*os.Root{}
	defer func() {
		for _, root := range roots {
			root.Close()
		}
	}()
	open := func(library Library) (*os.Root, error) {
		if root := roots[library.ID]; root != nil {
			return root, nil
		}
		root, err := openLibraryRoot(library.Root, library.RootIdentity)
		if err == nil {
			roots[library.ID] = root
		}
		return root, err
	}
	type prepared struct {
		target  RegistrationTarget
		library Library
		asset   discoveredAsset
	}
	items := make([]prepared, 0, len(targets))
	directories := map[string]directorySidecars{}
	seen := map[string]bool{}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !registrationAbsolute(target.Path) || target.Identity == "" || target.Size <= 0 || videoExtensions[strings.ToLower(filepath.Ext(target.Path))] == "" || (target.PreviousPath != "" && !registrationAbsolute(target.PreviousPath)) || seen[target.Path] {
			return result, ErrInvalid
		}
		seen[target.Path] = true
		library, relative, found := registrationRelative(libraries, target.Path)
		if !found {
			return result, ErrUnregistered
		}
		root, err := open(library)
		if err != nil {
			return result, err
		}
		info, err := registrationInfo(root, relative, target)
		if err != nil {
			return result, err
		}
		key := strconv.FormatInt(library.ID, 10) + ":" + path.Dir(relative)
		directory, cached := directories[key]
		if !cached {
			directory, err = sidecarDirectory(root, path.Dir(relative))
			if err != nil {
				return result, err
			}
			directories[key] = directory
		}
		asset, err := s.inspectAsset(root, library.ID, relative, info, directory)
		if err != nil {
			return result, err
		}
		items = append(items, prepared{target: target, library: library, asset: asset})
	}
	// Re-check every exact output after sidecar reads. No library directory IO
	// happens while holding the SQL transaction or blocking cancellation writes.
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		check, err := openLibraryRoot(item.library.Root, item.library.RootIdentity)
		if err != nil {
			return result, err
		}
		_, err = registrationInfo(check, item.asset.Path, item.target)
		check.Close()
		if err != nil {
			return result, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	batches := map[int64][]discoveredAsset{}
	for _, item := range items {
		asset := item.asset
		if item.target.PreviousPath != "" && item.target.PreviousPath != item.target.Path {
			previous, relative, found := registrationRelative(libraries, item.target.PreviousPath)
			if found {
				// Only a missing old path and a matching stored fingerprint prove
				// a move. A still-existing source could be a copy or hard link.
				root, err := open(previous)
				if err != nil {
					return result, err
				}
				if _, err := root.Lstat(relative); !os.IsNotExist(err) {
					return result, ErrConflict
				}
				var oldID string
				err = tx.QueryRowContext(ctx, "SELECT id FROM catalog_assets WHERE library_id=? AND path=? AND file_identity=? ORDER BY id LIMIT 1", previous.ID, relative, asset.FileIdentity).Scan(&oldID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return result, err
				}
				if oldID != "" {
					asset.ID = oldID
				}
			}
		}
		batches[item.library.ID] = append(batches[item.library.ID], asset)
	}
	transfers := map[string]map[string]bool{}
	stamp, token := timestamp(), rand.Text()
	for libraryID, assets := range batches {
		if _, err := s.publishAssets(ctx, tx, libraryID, assets, stamp, token, transfers); err != nil {
			return result, err
		}
		result.LibraryIDs = append(result.LibraryIDs, libraryID)
		for _, asset := range assets {
			result.WorkIDs = append(result.WorkIDs, asset.WorkID)
		}
	}
	for oldID, destinations := range transfers {
		if err := transferOrphanedPersonal(ctx, tx, oldID, destinations); err != nil {
			return result, err
		}
	}
	if err := syncActorsTx(ctx, tx); err != nil {
		return result, err
	}
	sort.Slice(result.LibraryIDs, func(i, j int) bool { return result.LibraryIDs[i] < result.LibraryIDs[j] })
	sort.Strings(result.WorkIDs)
	result.WorkIDs = uniqueText(result.WorkIDs)
	result.Assets, result.Works = len(items), len(result.WorkIDs)
	if finish != nil {
		if err := finish(tx, result); err != nil {
			return RegistrationResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RegistrationResult{}, err
	}
	return result, nil
}
