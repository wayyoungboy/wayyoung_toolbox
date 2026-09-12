package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func readIntakeOperation(source, destination *os.Root, relative string, jobID int64) (intakeCleanupJournal, error) {
	var journal intakeCleanupJournal
	name := filepath.Join(".media-center-intake", intakeOperationName(destination, relative, jobID), "operation.json")
	if err := rejectIntakeLinks(source, name, false); err != nil {
		return journal, err
	}
	file, err := source.Open(name)
	if err != nil {
		return journal, err
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 64<<20)).Decode(&journal); err != nil {
		return journal, err
	}
	if journal.Relative != relative || journal.DestinationRoot != destination.Name() || journal.JobID != jobID {
		return journal, fmt.Errorf("搬运凭据不匹配")
	}
	return journal, nil
}

// The receipt is committed before rename, and bound to the persistent queue
// job and original inode. A retry never infers success merely from target
// existence. All production callers hold Service.mediaFiles.
func moveIntakeWithReceipt(ctx context.Context, source, destination *os.Root, relative string, jobID int64) (string, error) {
	journal, err := readIntakeOperation(source, destination, relative, jobID)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("%w：%w", errIntakePending, err)
	}
	newOperation := os.IsNotExist(err)
	if !newOperation && journal.State == "done" {
		return "moved", nil
	}
	if newOperation {
		if err := rejectIntakeLinks(source, relative, false); err != nil {
			return "", err
		}
		info, err := source.Lstat(relative)
		if err != nil {
			return "", err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return "", fmt.Errorf("拒绝搬运特殊文件")
		}
		identity := intakeFileIdentity(info)
		if identity == "" {
			return "", fmt.Errorf("当前系统不支持文件身份校验")
		}
		journal = intakeCleanupJournal{JobID: jobID, Outcome: "moved", Relative: relative, DestinationRoot: destination.Name(), State: "moving", Identity: identity}
	}
	if journal.State != "moving" || journal.Outcome != "moved" || journal.Identity == "" {
		return "", fmt.Errorf("%w：无效的搬运阶段", errIntakePending)
	}
	private, err := openIntakePrivateDirectory(source, ".media-center-intake")
	if err != nil {
		return "", err
	}
	defer private.Close()
	op, err := openIntakePrivateDirectory(private, intakeOperationName(destination, relative, jobID))
	if err != nil {
		return "", err
	}
	defer op.Close()
	if newOperation {
		if err := saveIntakeJournal(op, journal); err != nil {
			return "", fmt.Errorf("%w：%w", errIntakePending, err)
		}
	}
	if err := finishIntakeMove(ctx, source, destination, relative, journal.Identity); err != nil {
		return "", fmt.Errorf("%w：%w", errIntakePending, err)
	}
	journal.State = "done"
	if err := saveIntakeJournal(op, journal); err != nil {
		return "", fmt.Errorf("%w：%w", errIntakePending, err)
	}
	return "moved", nil
}

func finishIntakeMove(ctx context.Context, source, destination *os.Root, relative, identity string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rejectIntakeLinks(destination, relative, true); err != nil {
		return err
	}
	if info, err := destination.Lstat(relative); err == nil {
		if intakeFileIdentity(info) != identity {
			return errIntakeConflict
		}
		if _, err := source.Lstat(relative); !os.IsNotExist(err) {
			return fmt.Errorf("目标已搬运但来源路径被重新占用，请人工处理")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if err := rejectIntakeLinks(source, relative, false); err != nil {
			return err
		}
		info, err := source.Lstat(relative)
		if err != nil {
			return err
		}
		if intakeFileIdentity(info) != identity {
			return fmt.Errorf("来源文件身份已变化")
		}
		if err := destination.MkdirAll(filepath.Dir(relative), 0750); err != nil {
			return err
		}
		if err := syncIntakePathParents(destination, relative); err != nil {
			return err
		}
		if err := renameIntakeNoReplace(source, relative, destination, relative); err != nil {
			return err
		}
	}
	if err := syncIntakePathParents(destination, relative); err != nil {
		return err
	}
	return syncIntakePathParents(source, relative)
}

func syncIntakePathParents(root *os.Root, relative string) error {
	for parent := filepath.Dir(relative); ; parent = filepath.Dir(parent) {
		file, err := root.Open(parent)
		if err != nil {
			return err
		}
		err = file.Sync()
		file.Close()
		if err != nil {
			return err
		}
		if parent == "." {
			return nil
		}
	}
}
