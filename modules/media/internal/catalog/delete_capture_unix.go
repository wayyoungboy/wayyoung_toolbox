//go:build linux || darwin

package catalog

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// Capture through pinned directory descriptors before checking identity. An
// external rename cannot make us unlink an unpreviewed leaf. The private
// staging directory exists only during this operation, not as a recycle bin.
func removeCapturedFile(parent *os.Root, name, expected string) error {
	staging := ".catalog-delete-" + rand.Text()
	if err := parent.Mkdir(staging, 0700); err != nil {
		return err
	}
	created, err := parent.Lstat(staging)
	if err != nil || !privateDeletionDirectory(created) {
		return ErrConflict
	}
	stage, err := parent.OpenRoot(staging)
	if err != nil {
		return err
	}
	defer stage.Close()
	opened, err := stage.Stat(".")
	if err != nil || !os.SameFile(created, opened) || !privateDeletionDirectory(opened) {
		return ErrConflict
	}
	parentFD, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer parentFD.Close()
	stageFD, err := stage.Open(".")
	if err != nil {
		return err
	}
	defer stageFD.Close()
	entries, err := stageFD.ReadDir(-1)
	if err != nil || len(entries) != 0 {
		return ErrConflict
	}
	receipt, err := stage.OpenFile("receipt.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writeErr := json.NewEncoder(receipt).Encode(map[string]string{"original_name": name, "fingerprint": expected, "captured_name": "file"})
	if writeErr == nil {
		writeErr = receipt.Sync()
	}
	closeErr := receipt.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := stageFD.Sync(); err != nil {
		return err
	}
	cleanup := func() { _ = stage.Remove("receipt.json"); _ = parent.Remove(staging) }
	if err := captureRename(int(parentFD.Fd()), name, int(stageFD.Fd()), "file"); err != nil {
		cleanup()
		return err
	}
	restore := func(cause error) error {
		// linkat is no-replace: never overwrite another file at the original name.
		// A raced directory cannot be hard-linked and is left intact for inspection.
		if err := unix.Linkat(int(stageFD.Fd()), "file", int(parentFD.Fd()), name, 0); err != nil {
			return fmt.Errorf("%w; captured file retained at %s/file (restore: %v)", cause, staging, err)
		}
		if err := stage.Remove("file"); err != nil {
			return fmt.Errorf("%w; retained staging copy %s/file", cause, staging)
		}
		cleanup()
		return cause
	}
	if err := parentFD.Sync(); err != nil {
		return restore(err)
	}
	if err := stageFD.Sync(); err != nil {
		return restore(err)
	}
	info, err := stage.Lstat("file")
	if err != nil {
		return restore(err)
	}
	if !info.Mode().IsRegular() {
		return restore(ErrConflict)
	}
	identity, err := directoryIdentity(info)
	if err != nil {
		return restore(err)
	}
	fingerprint := stableID(identity, strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10))
	if fingerprint != expected {
		return restore(ErrConflict)
	}
	if err := stage.Remove("file"); err != nil {
		return restore(err)
	}
	cleanup()
	return nil
}

func privateDeletionDirectory(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}
