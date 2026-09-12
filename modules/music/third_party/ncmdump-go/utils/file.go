package utils

import (
	"os"
	"path/filepath"
	"strings"
)

func ReplaceExtension(filepathStr, newExt string) string {
	ext := filepath.Ext(filepathStr)
	return strings.TrimSuffix(filepathStr, ext) + newExt
}

func PathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return false
}

func IsDir(path string) bool {
	s, err := os.Stat(path)
	if err != nil {

		return false
	}
	return s.IsDir()
}

func GetRelativePath(from, to string) string {
	rel, err := filepath.Rel(from, to)
	if err != nil {
		return ""
	}
	return rel
}

func IsRegularFile(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.Mode().IsRegular()
}
