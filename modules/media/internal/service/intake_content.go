package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	errIntakeConflict = errors.New("目标已存在但未通过完整内容核验，请人工处理")
	errIntakePending  = errors.New("来源已接管，待恢复文件处理")
)

type intakeContentEntry struct {
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	Hash      string `json:"sha256,omitempty"`
}

type intakeCleanupJournal struct {
	JobID           int64                         `json:"job_id,omitempty"`
	Outcome         string                        `json:"outcome,omitempty"`
	Identity        string                        `json:"identity,omitempty"`
	Relative        string                        `json:"relative"`
	DestinationRoot string                        `json:"destination_root"`
	State           string                        `json:"state"`
	Entries         map[string]intakeContentEntry `json:"entries,omitempty"`
}

func intakeOperationName(destination *os.Root, relative string, jobID int64) string {
	keyText := destination.Name() + "\x00" + relative
	if jobID != 0 {
		keyText += fmt.Sprintf("\x00job:%d", jobID)
	}
	key := sha256.Sum256([]byte(keyText))
	return hex.EncodeToString(key[:])
}

func intakeOperationExists(source, destination *os.Root, relative string, jobID int64) (bool, error) {
	name := filepath.Join(".media-center-intake", intakeOperationName(destination, relative, jobID), "operation.json")
	if err := rejectIntakeLinks(source, name, false); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Duplicate cleanup takes ownership of the source before checking it. The
// private, same-filesystem staging directory and durable journal survive restart
// and prevent a replacement at the public source path from being deleted.
func removeVerifiedIntakeDuplicate(ctx context.Context, source, destination *os.Root, relative string, jobID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	private, err := openIntakePrivateDirectory(source, ".media-center-intake")
	if err != nil {
		return err
	}
	defer private.Close()
	opName := intakeOperationName(destination, relative, jobID)
	op, err := openIntakePrivateDirectory(private, opName)
	if err != nil {
		return err
	}
	defer op.Close()
	journal := intakeCleanupJournal{JobID: jobID, Outcome: "duplicate_removed", Relative: relative, DestinationRoot: destination.Name(), State: "capturing"}
	if file, readErr := op.Open("operation.json"); readErr == nil {
		decoder := json.NewDecoder(io.LimitReader(file, 64<<20))
		readErr = decoder.Decode(&journal)
		file.Close()
		if readErr != nil || journal.Relative != relative || journal.DestinationRoot != destination.Name() || journal.JobID != jobID {
			return fmt.Errorf("%w：接管记录无效", errIntakePending)
		}
	} else if os.IsNotExist(readErr) {
		if err := saveIntakeJournal(op, journal); err != nil {
			return err
		}
	} else {
		return readErr
	}
	finish := func() error {
		if err := op.Remove("operation.next"); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := op.Remove("operation.json"); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := private.Remove(opName); err != nil {
			return err
		}
		return syncIntakeDirectory(private)
	}
	if journal.State == "done" {
		return nil
	}
	if journal.State == "restored" {
		if _, err := op.Lstat("content"); !os.IsNotExist(err) {
			return fmt.Errorf("%w：已恢复事务仍有隔离内容", errIntakePending)
		}
		if err := rejectIntakeLinks(source, relative, false); err != nil {
			return err
		}
		journal.State = "capturing"
		journal.Entries = nil
		if err := saveIntakeJournal(op, journal); err != nil {
			return err
		}
	}
	if journal.State == "restoring" {
		if _, err := op.Lstat("content"); err == nil {
			if err := renameIntakeNoReplace(op, "content", source, relative); err != nil {
				return fmt.Errorf("%w：恢复来源：%w", errIntakePending, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("%w：%w", errIntakePending, err)
		} else if err := rejectIntakeLinks(source, relative, false); err != nil {
			return fmt.Errorf("%w：恢复来源无法确认：%w", errIntakePending, err)
		}
		journal.State = "restored"
		if err := saveIntakeJournal(op, journal); err != nil {
			return fmt.Errorf("%w：%w", errIntakePending, err)
		}
		return errIntakeConflict
	}
	if journal.State == "capturing" {
		if _, err := op.Lstat("content"); os.IsNotExist(err) {
			if err := rejectIntakeLinks(source, relative, false); err != nil {
				_ = finish()
				return err
			}
			if err := renameIntakeNoReplace(source, relative, op, "content"); err != nil {
				if !errors.Is(err, errIntakePending) {
					_ = finish()
				}
				return err
			}
			if err := syncIntakeDirectory(op); err != nil {
				return fmt.Errorf("%w：%w", errIntakePending, err)
			}
		} else if err != nil {
			return err
		}
		// A crash after rename but before this journal write is resolved by the
		// existence of content, never by guessing that a public destination is ours.
		journal.State = "captured"
		if err := saveIntakeJournal(op, journal); err != nil {
			return fmt.Errorf("%w：%w", errIntakePending, err)
		}
	}
	if journal.State == "captured" {
		if _, err := destination.Lstat(relative); err != nil {
			return fmt.Errorf("%w：目标不可访问：%w", errIntakePending, err)
		}
		left, err := intakeContentManifest(ctx, op, "content")
		if err == nil {
			var right map[string]intakeContentEntry
			right, err = intakeContentManifest(ctx, destination, relative)
			if err == nil && !equalIntakeManifests(left, right) {
				err = errIntakeConflict
			}
		}
		if err != nil {
			journal.State = "restoring"
			if saveErr := saveIntakeJournal(op, journal); saveErr != nil {
				return fmt.Errorf("%w：%w", errIntakePending, saveErr)
			}
			if restoreErr := renameIntakeNoReplace(op, "content", source, relative); restoreErr != nil {
				return fmt.Errorf("%w（%s）：%v；恢复原路径失败：%v", errIntakePending, filepath.Join(source.Name(), ".media-center-intake", opName), err, restoreErr)
			}
			journal.State = "restored"
			if cleanupErr := saveIntakeJournal(op, journal); cleanupErr != nil {
				return fmt.Errorf("来源已恢复，清理接管记录失败：%w", cleanupErr)
			}
			return err
		}
		journal.State = "cleaning"
		journal.Entries = left
		if err := saveIntakeJournal(op, journal); err != nil {
			return fmt.Errorf("%w：%w", errIntakePending, err)
		}
	}
	if journal.State != "cleaning" {
		return fmt.Errorf("%w：未知文件处理阶段", errIntakePending)
	}
	if err := cleanIntakeCaptured(ctx, op, destination, relative, journal.Entries); err != nil {
		return fmt.Errorf("%w（%s）：%w", errIntakePending, filepath.Join(source.Name(), ".media-center-intake", opName), err)
	}
	journal.State = "done"
	if err := saveIntakeJournal(op, journal); err != nil {
		return fmt.Errorf("%w：%w", errIntakePending, err)
	}
	// Keep the receipt until the persistent queue acknowledges qB cleanup. A
	// disappearing source or a later organization is not evidence of failure.
	return nil
}

func openIntakePrivateDirectory(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("接管目录必须为应用私有目录")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	current, err := file.Stat()
	file.Close()
	if err != nil || !os.SameFile(info, current) || !intakeDirectoryOwned(current) {
		root.Close()
		return nil, fmt.Errorf("接管目录身份校验失败")
	}
	if err := syncIntakeDirectory(parent); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func syncIntakeDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func saveIntakeJournal(root *os.Root, journal intakeCleanupJournal) error {
	file, err := root.OpenFile("operation.next", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(file).Encode(journal)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename("operation.next", "operation.json"); err != nil {
		return err
	}
	return syncIntakeDirectory(root)
}

func equalIntakeManifests(left, right map[string]intakeContentEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for path, entry := range left {
		if other, ok := right[path]; !ok || entry != other {
			return false
		}
	}
	return true
}

// Only known entries in the captured private source are removed. Missing source
// entries mean a prior attempt already removed them; the target is revalidated.
func cleanIntakeCaptured(ctx context.Context, op, destination *os.Root, relative string, entries map[string]intakeContentEntry) error {
	if len(entries) == 0 {
		return errIntakeConflict
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		if path != "" && (!filepath.IsLocal(path) || hasParentSegment(path)) {
			return errIntakeConflict
		}
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		expected := entries[path]
		dst := filepath.Join(relative, path)
		info, err := destination.Lstat(dst)
		if err != nil {
			return err
		}
		actual, err := intakeEntry(ctx, destination, dst, info)
		if err != nil {
			return err
		}
		if actual != expected {
			return errIntakeConflict
		}
		src := filepath.Join("content", path)
		info, err = op.Lstat(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		actual, err = intakeEntry(ctx, op, src, info)
		if err != nil {
			return err
		}
		if actual != expected {
			return errIntakeConflict
		}
		// Revalidate the destination after reading the captured source as well.
		// Production callers additionally hold the shared media-files guard so
		// JAVSP and maintenance cannot move/delete the verified destination.
		info, err = destination.Lstat(dst)
		if err != nil {
			return err
		}
		actual, err = intakeEntry(ctx, destination, dst, info)
		if err != nil {
			return err
		}
		if actual != expected {
			return errIntakeConflict
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := op.Remove(src); err != nil {
			return err
		}
	}
	return syncIntakeDirectory(op)
}

func intakeContentManifest(ctx context.Context, root *os.Root, name string) (map[string]intakeContentEntry, error) {
	entries := make(map[string]intakeContentEntry)
	err := fs.WalkDir(root.FS(), name, func(path string, directory fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if len(entries) >= maxMaintenanceEntries {
			return fmt.Errorf("内容核验超过 %d 个条目", maxMaintenanceEntries)
		}
		info, err := directory.Info()
		if err != nil {
			return err
		}
		entry, err := intakeEntry(ctx, root, path, info)
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(path, name), "/")
		entries[relative] = entry
		return nil
	})
	return entries, err
}

func intakeEntry(ctx context.Context, root *os.Root, path string, info fs.FileInfo) (intakeContentEntry, error) {
	entry := intakeContentEntry{Directory: info.IsDir()}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return entry, fmt.Errorf("%w：包含链接或特殊文件", errIntakeConflict)
	}
	if info.IsDir() {
		return entry, nil
	}
	sum, err := hashIntakeFile(ctx, root, path, info)
	entry.Size = info.Size()
	entry.Hash = hex.EncodeToString(sum[:])
	return entry, err
}

func hashIntakeFile(ctx context.Context, root *os.Root, path string, expected fs.FileInfo) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	if err := ctx.Err(); err != nil {
		return sum, err
	}
	file, err := root.Open(path)
	if err != nil {
		return sum, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || !os.SameFile(expected, before) || before.Size() != expected.Size() || !before.ModTime().Equal(expected.ModTime()) {
		return sum, fmt.Errorf("%w：核验时文件发生变化", errIntakeConflict)
	}
	hash := sha256.New()
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return sum, readErr
		}
	}
	after, err := file.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return sum, fmt.Errorf("%w：读取时文件发生变化", errIntakeConflict)
	}
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}
