//go:build !linux && !darwin

package service

import "io/fs"

func intakeFileIdentity(info fs.FileInfo) string { return "" }

func intakeDirectoryOwned(_ fs.FileInfo) bool { return false }
