package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxScanEntries = 200000

// A conservative logical staging budget, including paths, indexes and JSON.
// This is not a physical SQLite/WAL disk quota.
const maxScanStageBytes = 128 << 20

type scanSidecars struct {
	Files  map[string]string
	Videos []string
}
type scanDirectoryCache struct {
	JobID, Version int64
	Path           string
	Index          directorySidecars
}
type scanDirectory struct {
	Path, Identity, Sidecars, Manifest string
	ModifiedNS                         int64
}
type scanStagedFile struct {
	Asset    *discoveredAsset
	Proof    string
	Sidecars map[string]string
}

func scanSignature(info fs.FileInfo) (string, error) {
	identity, err := directoryIdentity(info)
	if err != nil {
		return "", err
	}
	return stableID(identity, strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10)), nil
}

func scanDirectoryInfo(root *os.Root, relative string) (fs.FileInfo, error) {
	if !fs.ValidPath(relative) {
		return nil, ErrInvalid
	}
	parts := strings.Split(relative, "/")
	for i := 1; i <= len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i], "/"))
		if err != nil {
			return nil, ErrUnavailable
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrConflict
		}
		if i == len(parts) {
			return info, nil
		}
	}
	return nil, ErrInvalid
}

func verifyScanDirectory(root *os.Root, directory scanDirectory) error {
	info, err := scanDirectoryInfo(root, directory.Path)
	if err != nil {
		return err
	}
	identity, err := directoryIdentity(info)
	if err != nil {
		return err
	}
	if identity != directory.Identity || info.ModTime().UnixNano() != directory.ModifiedNS {
		return ErrConflict
	}
	return nil
}

func scanLeafProof(root *os.Root, relative string) (string, error) {
	if !fs.ValidPath(relative) || relative == "." {
		return "", ErrInvalid
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return "", ErrUnavailable
	}
	if !info.Mode().IsRegular() {
		return "", ErrConflict
	}
	return scanSignature(info)
}

func scanSidecarProof(root *os.Root, relative string) (string, error) {
	proof, err := scanLeafProof(root, relative)
	if err != nil || !strings.EqualFold(path.Ext(relative), ".nfo") {
		return proof, err
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return "", ErrUnavailable
	}
	if info.Size() > maxNFOBytes {
		// Oversized NFOs are deliberately not parsed. Keep the video's filename
		// fallback and issue rather than allocate an unbounded sidecar.
		return proof + ":oversized", nil
	}
	raw, err := readBounded(root, relative, maxNFOBytes)
	if err != nil {
		return "", err
	}
	after, err := scanLeafProof(root, relative)
	if err != nil {
		return "", err
	}
	if proof != after {
		return "", ErrConflict
	}
	hash := sha256.Sum256(raw)
	return proof + ":" + hex.EncodeToString(hash[:]), nil
}

func scanManifestEntry(entry fs.DirEntry) string {
	return entry.Name() + "\x00" + strconv.FormatUint(uint64(entry.Type()), 16)
}

func scanManifest(entries []string) string {
	sort.Strings(entries)
	hash := sha256.New()
	for _, entry := range entries {
		hash.Write([]byte(entry))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func verifyScanManifest(ctx context.Context, root *os.Root, directory scanDirectory) error {
	if directory.Manifest == "" {
		return ErrConflict
	}
	if err := verifyScanDirectory(root, directory); err != nil {
		return err
	}
	before, err := scanDirectoryInfo(root, directory.Path)
	if err != nil {
		return err
	}
	file, err := root.Open(directory.Path)
	if err != nil {
		return ErrUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return ErrConflict
	}
	names := []string{}
	budget := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := file.ReadDir(256)
		for _, entry := range entries {
			budget += int64(64 + 2*len(entry.Name()))
			if len(names) >= maxScanEntries || budget > maxScanStageBytes || !utf8.ValidString(entry.Name()) {
				return ErrInvalid
			}
			names = append(names, scanManifestEntry(entry))
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return ErrUnavailable
		}
	}
	if scanManifest(names) != directory.Manifest {
		return ErrConflict
	}
	return verifyScanDirectory(root, directory)
}

func (s *Store) checkScanTx(ctx context.Context, tx *sql.Tx, job ScanJob) error {
	current, err := readScanJob(tx.QueryRowContext(ctx, "SELECT "+scanJobColumns+" FROM catalog_scan_jobs WHERE id=?", job.ID))
	if err != nil {
		return err
	}
	if current.Version != job.Version || current.Status != "running" {
		return ErrConflict
	}
	var root, identity string
	if err := tx.QueryRowContext(ctx, "SELECT root,root_identity FROM catalog_libraries WHERE id=?", job.LibraryID).Scan(&root, &identity); err != nil {
		return err
	}
	if root != job.Root || identity != job.RootIdentity {
		return ErrConflict
	}
	return nil
}

// ScanBatch saves a bounded file batch or one directory listing. The caller
// owns mediaFiles for the entire attempt; scanMu serializes catalog publication.
// No partial checkpoint changes the published catalog or performs a sweep.
func (s *Store) ScanBatch(ctx context.Context, id, version int64, limit int) (result ScanJob, resultErr error) {
	if id < 1 || version < 1 || limit < 1 || limit > 256 {
		return result, ErrInvalid
	}
	if !s.scanMu.TryLock() {
		return result, ErrBusy
	}
	defer s.scanMu.Unlock()
	job, err := s.ScanJob(ctx, id)
	if err != nil {
		return result, err
	}
	if job.Version != version || job.Status != "running" {
		return job, ErrConflict
	}
	result = job
	defer func() {
		if resultErr == nil || errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) || errors.Is(resultErr, ErrBusy) {
			return
		}
		failed, err := s.failScan(ctx, job, resultErr)
		if err == nil {
			result = failed
		}
	}()
	if err := ctx.Err(); err != nil {
		return job, err
	}
	library, err := s.library(ctx, job.LibraryID)
	if err != nil {
		return job, err
	}
	if library.Root != job.Root || library.RootIdentity != job.RootIdentity {
		return job, ErrConflict
	}
	root, err := openLibraryRoot(job.Root, job.RootIdentity)
	if err != nil {
		return job, err
	}
	defer root.Close()
	var parent string
	err = s.db.QueryRowContext(ctx, "SELECT parent FROM catalog_scan_files WHERE job_id=? AND processed=0 ORDER BY parent,path LIMIT 1", id).Scan(&parent)
	if err == nil {
		return s.scanFileBatch(ctx, root, job, parent, limit)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job, err
	}
	var directory string
	err = s.db.QueryRowContext(ctx, "SELECT path FROM catalog_scan_dirs WHERE job_id=? AND listed=0 ORDER BY length(path),path LIMIT 1", id).Scan(&directory)
	if err == nil {
		return s.scanDirectoryBatch(ctx, root, job, directory)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job, err
	}
	return s.finishScan(ctx, root, job)
}

func (s *Store) scanDirectoryBatch(ctx context.Context, root *os.Root, job ScanJob, relative string) (ScanJob, error) {
	before, err := scanDirectoryInfo(root, relative)
	if err != nil {
		return job, err
	}
	identity, err := directoryIdentity(before)
	if err != nil {
		return job, err
	}
	file, err := root.Open(relative)
	if err != nil {
		return job, ErrUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return job, ErrConflict
	}
	index := scanSidecars{Files: map[string]string{}, Videos: []string{}}
	children, files := []string{}, []string{}
	manifest := []string{}
	// Charge before growing maps/slices, including frontier rows, parent paths,
	// index duplication and worst-case JSON escaping rather than JSON alone.
	stagedBytes := int64(1024 + 8*len(relative))
	if job.StagedBytes+stagedBytes > maxScanStageBytes {
		return job, ErrInvalid
	}
	visited := 0
	for {
		if err := ctx.Err(); err != nil {
			return job, err
		}
		entries, readErr := file.ReadDir(256)
		for _, entry := range entries {
			visited++
			if job.Entries+visited > maxScanEntries {
				return job, ErrInvalid
			}
			name := entry.Name()
			full := path.Join(relative, name)
			if !utf8.ValidString(full) || !fs.ValidPath(full) || len(full) > 4096 {
				return job, ErrInvalid
			}
			stagedBytes += int64(1024 + 12*(len(full)+len(name)) + 8*len(relative))
			if job.StagedBytes+stagedBytes > maxScanStageBytes {
				return job, ErrInvalid
			}
			manifest = append(manifest, scanManifestEntry(entry))
			if strings.HasPrefix(strings.ToLower(name), ".catalog-delete-") {
				return job, ErrDeletePending
			}
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				if !skippedDirectory(name) {
					children = append(children, full)
				}
				continue
			}
			key := strings.ToLower(name)
			if previous, ok := index.Files[key]; ok && previous != full {
				index.Files[key] = ""
			} else {
				index.Files[key] = full
			}
			if videoExtensions[strings.ToLower(path.Ext(name))] != "" {
				files = append(files, full)
				index.Videos = append(index.Videos, name)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return job, ErrUnavailable
		}
	}
	directory := scanDirectory{Path: relative, Identity: identity, ModifiedNS: before.ModTime().UnixNano(), Manifest: scanManifest(manifest)}
	if err := verifyScanDirectory(root, directory); err != nil {
		return job, err
	}
	raw := jsonText(index)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
	if err := s.checkScanTx(ctx, tx, job); err != nil {
		return job, err
	}
	for _, child := range children {
		if _, err := tx.ExecContext(ctx, "INSERT INTO catalog_scan_dirs(job_id,path) VALUES(?,?)", job.ID, child); err != nil {
			return job, err
		}
	}
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, "INSERT INTO catalog_scan_files(job_id,path,parent) VALUES(?,?,?)", job.ID, file, relative); err != nil {
			return job, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_scan_dirs SET listed=1,identity=?,modified_ns=?,sidecars=?,manifest=? WHERE job_id=? AND path=? AND listed=0", identity, directory.ModifiedNS, raw, directory.Manifest, job.ID, relative); err != nil {
		return job, err
	}
	job.Entries += visited
	job.DiscoveredFiles += len(files)
	job.StagedBytes += stagedBytes
	job.CurrentPath = relative
	job.UpdatedAt = timestamp()
	if err := saveScanJobTx(ctx, tx, job, job.Version); err != nil {
		return job, err
	}
	if err := tx.Commit(); err != nil {
		return job, err
	}
	s.scanCache = &scanDirectoryCache{JobID: job.ID, Version: job.Version, Path: relative, Index: directorySidecars{files: index.Files, videos: index.Videos}}
	return job, nil
}

func (s *Store) scanFileBatch(ctx context.Context, root *os.Root, job ScanJob, parent string, limit int) (ScanJob, error) {
	directory := scanDirectory{Path: parent}
	if err := s.db.QueryRowContext(ctx, "SELECT identity,modified_ns,sidecars FROM catalog_scan_dirs WHERE job_id=? AND path=? AND listed=1", job.ID, parent).Scan(&directory.Identity, &directory.ModifiedNS, &directory.Sidecars); err != nil {
		return job, err
	}
	if err := verifyScanDirectory(root, directory); err != nil {
		return job, err
	}
	if s.scanCache == nil || s.scanCache.JobID != job.ID || s.scanCache.Version != job.Version || s.scanCache.Path != parent {
		var index scanSidecars
		if err := json.Unmarshal([]byte(directory.Sidecars), &index); err != nil {
			return job, err
		}
		s.scanCache = &scanDirectoryCache{JobID: job.ID, Version: job.Version, Path: parent, Index: directorySidecars{files: index.Files, videos: index.Videos}}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT path FROM catalog_scan_files WHERE job_id=? AND processed=0 AND parent=? ORDER BY path LIMIT ?", job.ID, parent, limit)
	if err != nil {
		return job, err
	}
	paths := []string{}
	for rows.Next() {
		var filename string
		if err := rows.Scan(&filename); err != nil {
			rows.Close()
			return job, err
		}
		paths = append(paths, filename)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return job, err
	}
	prepared := map[string]string{}
	for _, filename := range paths {
		if err := ctx.Err(); err != nil {
			return job, err
		}
		proof, err := scanLeafProof(root, filename)
		if err != nil {
			return job, err
		}
		info, err := root.Lstat(filename)
		if err != nil {
			return job, ErrUnavailable
		}
		staged := scanStagedFile{Proof: proof, Sidecars: map[string]string{}}
		if info.Size() > 0 {
			nfo := s.nfoSidecar(s.scanCache.Index, path.Base(filename), s.code(strings.TrimSuffix(path.Base(filename), path.Ext(filename))))
			if nfo != "" {
				value, err := scanSidecarProof(root, nfo)
				if err != nil {
					return job, err
				}
				staged.Sidecars[nfo] = value
			}
			asset, err := s.inspectAsset(root, job.LibraryID, filename, info, s.scanCache.Index)
			if err != nil {
				return job, err
			}
			staged.Asset = &asset
			used := []string{asset.NFOPath, asset.PosterPath}
			for _, subtitle := range asset.Subtitles {
				used = append(used, subtitle.Path)
			}
			for _, sidecar := range used {
				if sidecar == "" {
					continue
				}
				value, err := scanSidecarProof(root, sidecar)
				if err != nil {
					return job, err
				}
				if old, ok := staged.Sidecars[sidecar]; ok && old != value {
					return job, ErrConflict
				}
				staged.Sidecars[sidecar] = value
			}
		}
		current, err := scanLeafProof(root, filename)
		if err != nil {
			return job, err
		}
		if current != proof {
			return job, ErrConflict
		}
		raw := jsonText(staged)
		job.StagedBytes += int64(512 + len(raw) + 6*len(filename))
		if job.StagedBytes > maxScanStageBytes {
			return job, ErrInvalid
		}
		prepared[filename] = raw
	}
	if err := verifyScanDirectory(root, directory); err != nil {
		return job, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
	if err := s.checkScanTx(ctx, tx, job); err != nil {
		return job, err
	}
	for _, filename := range paths {
		if _, err := tx.ExecContext(ctx, "UPDATE catalog_scan_files SET processed=1,data=? WHERE job_id=? AND path=? AND processed=0", prepared[filename], job.ID, filename); err != nil {
			return job, err
		}
		job.ProcessedFiles++
		job.CurrentPath = filename
	}
	job.UpdatedAt = timestamp()
	if err := saveScanJobTx(ctx, tx, job, job.Version); err != nil {
		return job, err
	}
	if err := tx.Commit(); err != nil {
		return job, err
	}
	return job, nil
}

func (s *Store) verifyScanDirectories(ctx context.Context, root *os.Root, jobID int64) error {
	after := ""
	for {
		rows, err := s.db.QueryContext(ctx, "SELECT path,identity,modified_ns,manifest FROM catalog_scan_dirs WHERE job_id=? AND path>? ORDER BY path LIMIT 64", jobID, after)
		if err != nil {
			return err
		}
		directories := []scanDirectory{}
		for rows.Next() {
			var directory scanDirectory
			if err := rows.Scan(&directory.Path, &directory.Identity, &directory.ModifiedNS, &directory.Manifest); err != nil {
				rows.Close()
				return err
			}
			directories = append(directories, directory)
		}
		err = rows.Err()
		rows.Close() // release the sole SQLite connection before filesystem I/O
		if err != nil {
			return err
		}
		for _, directory := range directories {
			if err := verifyScanManifest(ctx, root, directory); err != nil {
				return err
			}
			after = directory.Path
		}
		if len(directories) < 64 {
			return nil
		}
	}
}

func (s *Store) verifiedScanAssets(ctx context.Context, root *os.Root, jobID int64) ([]discoveredAsset, error) {
	assets := []discoveredAsset{}
	after := ""
	for {
		rows, err := s.db.QueryContext(ctx, "SELECT path,data FROM catalog_scan_files WHERE job_id=? AND processed=1 AND path>? ORDER BY path LIMIT 64", jobID, after)
		if err != nil {
			return nil, err
		}
		type candidate struct{ Path, Raw string }
		files := []candidate{}
		for rows.Next() {
			var file candidate
			if err := rows.Scan(&file.Path, &file.Raw); err != nil {
				rows.Close()
				return nil, err
			}
			files = append(files, file)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var staged scanStagedFile
			if err := json.Unmarshal([]byte(file.Raw), &staged); err != nil {
				return nil, err
			}
			proof, err := scanLeafProof(root, file.Path)
			if err != nil {
				return nil, err
			}
			if proof != staged.Proof {
				return nil, ErrConflict
			}
			for sidecar, expected := range staged.Sidecars {
				proof, err := scanSidecarProof(root, sidecar)
				if err != nil {
					return nil, err
				}
				if proof != expected {
					return nil, ErrConflict
				}
			}
			if staged.Asset != nil {
				assets = append(assets, *staged.Asset)
			}
			after = file.Path
		}
		if len(files) < 64 {
			return assets, nil
		}
	}
}

func (s *Store) finishScan(ctx context.Context, root *os.Root, job ScanJob) (ScanJob, error) {
	assets, err := s.verifiedScanAssets(ctx, root, job.ID)
	if err != nil {
		return job, err
	}
	if err := s.verifyScanDirectories(ctx, root, job.ID); err != nil {
		return job, err
	}
	// All I/O is complete before taking the sole SQLite connection's tx.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
	if err := s.checkScanTx(ctx, tx, job); err != nil {
		return job, err
	}
	stamp, token := timestamp(), rand.Text()
	transfers := map[string]map[string]bool{}
	count, err := s.publishAssets(ctx, tx, map[int64][]discoveredAsset{job.LibraryID: assets}, stamp, token, transfers)
	if err != nil {
		return job, err
	}
	for oldID, destinations := range transfers {
		if err := transferOrphanedPersonal(ctx, tx, oldID, destinations); err != nil {
			return job, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_assets SET available=0 WHERE library_id=? AND scan_token!=?", job.LibraryID, token); err != nil {
		return job, err
	}
	result := ScanResult{LibraryID: job.LibraryID, ScannedAt: stamp, Assets: len(assets), Works: count}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_assets WHERE library_id=? AND scan_token=? AND json_array_length(issues)>0", job.LibraryID, token).Scan(&result.Issues); err != nil {
		return job, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_libraries SET scanned_at=?,scan_error='' WHERE id=?", stamp, job.LibraryID); err != nil {
		return job, err
	}
	if err := syncActorsTx(ctx, tx); err != nil {
		return job, err
	}
	if err := ReconcileWantsTx(ctx, tx, acquiredAssetIDs(assets), "scan", 0, stamp); err != nil {
		return job, err
	}
	if err := clearScanStage(ctx, tx, job.ID); err != nil {
		return job, err
	}
	job.Status, job.Phase = "success", "done"
	job.Result = &result
	job.UpdatedAt = stamp
	job.StagedBytes = 0
	job.Version++
	if err := saveScanJobTx(ctx, tx, job, job.Version-1); err != nil {
		return job, err
	}
	if err := tx.Commit(); err != nil {
		return job, err
	}
	s.scanCache = nil
	return job, nil
}

func (s *Store) failScan(ctx context.Context, job ScanJob, cause error) (ScanJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
	if err := s.checkScanTx(ctx, tx, job); err != nil {
		// Root relinking is a valid reason to fail this old attempt, but a
		// lifecycle CAS change must retain the administrator's newer state.
		current, readErr := readScanJob(tx.QueryRowContext(ctx, "SELECT "+scanJobColumns+" FROM catalog_scan_jobs WHERE id=?", job.ID))
		if readErr != nil || current.Version != job.Version || current.Status != "running" {
			return job, ErrConflict
		}
	}
	if err := clearScanStage(ctx, tx, job.ID); err != nil {
		return job, err
	}
	job.Status = "failed"
	job.Error = "扫描未完成，原馆藏保留；请核对目录与文件后重试"
	job.StagedBytes = 0
	job.UpdatedAt = timestamp()
	job.Version++
	if errors.Is(cause, ErrDeletePending) {
		job.Error = "发现未完成的馆藏删除凭据，请人工检查后重新扫描"
	}
	if err := saveScanJobTx(ctx, tx, job, job.Version-1); err != nil {
		return job, err
	}
	if err := tx.Commit(); err != nil {
		return job, err
	}
	s.scanCache = nil
	return job, nil
}
