package service

import (
	"errors"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

// Linux permits byte filenames that JSON cannot losslessly represent. APFS
// rejects creating them, so this filesystem-specific contract runs on Linux.
func TestCatalogScanRejectsNonUTF8DirectoryEntryBeforeStaging(t *testing.T) {
	s, job, root := scanJobFixture(t)
	job = claimCatalogScan(t, s, job)
	catalogWrite(t, root, "BAD-\xff.mp4", "synthetic invalid filename")
	job, err := s.db.catalog.ScanBatch(t.Context(), job.ID, job.Version, 1)
	if !errors.Is(err, catalog.ErrInvalid) || job.Status != "failed" || len(catalogWorks(t, s, "")) != 0 {
		t.Fatalf("lossy filename staged or published: %+v %v", job, err)
	}
}
