package browserdata

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

func safeMember(name string) bool {
	return name != "" && !strings.ContainsAny(name, "\\\x00") && !strings.HasPrefix(name, "/") && path.Clean(name) == strings.TrimSuffix(name, "/") && name != ".." && !strings.HasPrefix(name, "../")
}
func supportedMember(name string) bool {
	name = strings.ToLower(path.Base(name))
	for _, suffix := range []string{".json", ".jsonl", ".html", ".htm", ".sqlite", ".db", ".gz", ".xz", ".zst", ".zstd", ".lz4", ".zip"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return name == "history" || name == "bookmarks" || name == "places.sqlite"
}
func (p *parser) decode(name string, data []byte, depth int) error {
	if depth > 3 {
		return ErrLimit
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	lower := strings.ToLower(name)
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) || strings.HasSuffix(lower, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return invalid("ZIP")
		}
		if len(zr.File) > 512 {
			return ErrLimit
		}
		seen := map[string]bool{}
		matched := 0
		for _, f := range zr.File {
			if !safeMember(f.Name) || seen[f.Name] || f.Mode()&^0o777 != 0 && !f.FileInfo().IsDir() {
				return invalid("ZIP member")
			}
			seen[f.Name] = true
			if f.UncompressedSize64 > uint64(MaxExpanded-p.expanded) {
				return ErrLimit
			}
			if f.FileInfo().IsDir() || !supportedMember(f.Name) {
				continue
			}
			if p.kind == "bookmarks" && strings.Contains(strings.ToLower(f.Name), "history") {
				continue
			}
			if p.kind == "history" && strings.Contains(strings.ToLower(f.Name), "bookmark") {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return invalid("ZIP member")
			}
			content, readErr := p.read(r)
			closeErr := r.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return invalid("ZIP member")
			}
			if err := p.decode(f.Name, content, depth+1); err != nil {
				return err
			}
			matched++
		}
		if matched == 0 {
			return invalid("ZIP has no matching browser records")
		}
		p.snapshot.Format = "archive"
		return nil
	}
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) || strings.HasSuffix(lower, ".gz") {
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return invalid("gzip")
		}
		content, readErr := p.read(r)
		closeErr := r.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return invalid("gzip")
		}
		return p.decode(strings.TrimSuffix(name, path.Ext(name)), content, depth+1)
	}
	if bytes.HasPrefix(data, []byte{0xfd, '7', 'z', 'X', 'Z', 0}) || strings.HasSuffix(lower, ".xz") {
		r, err := (xz.ReaderConfig{DictCap: 8 << 20}).NewReader(bytes.NewReader(data))
		if err != nil {
			return invalid("xz")
		}
		content, err := p.read(r)
		if err != nil {
			return err
		}
		return p.decode(strings.TrimSuffix(name, path.Ext(name)), content, depth+1)
	}
	if bytes.HasPrefix(data, []byte{0x28, 0xb5, 0x2f, 0xfd}) || strings.HasSuffix(lower, ".zst") || strings.HasSuffix(lower, ".zstd") {
		r, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(16<<20), zstd.WithDecoderMaxWindow(8<<20))
		if err != nil {
			return invalid("zstd")
		}
		defer r.Close()
		content, err := p.read(r)
		if err != nil {
			return err
		}
		return p.decode(strings.TrimSuffix(name, path.Ext(name)), content, depth+1)
	}
	if bytes.HasPrefix(data, []byte{4, 0x22, 0x4d, 0x18}) || strings.HasSuffix(lower, ".lz4") {
		content, err := p.read(lz4.NewReader(bytes.NewReader(data)))
		if err != nil {
			return err
		}
		return p.decode(strings.TrimSuffix(name, path.Ext(name)), content, depth+1)
	}
	if strings.HasSuffix(lower, ".tar") || len(data) > 262 && string(data[257:262]) == "ustar" {
		tr := tar.NewReader(bytes.NewReader(data))
		seen := map[string]bool{}
		matched := 0
		for count := 0; ; count++ {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return invalid("tar")
			}
			if count >= 512 {
				return ErrLimit
			}
			if !safeMember(h.Name) || seen[h.Name] || (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) {
				return invalid("tar member")
			}
			seen[h.Name] = true
			if h.Size > int64(MaxExpanded-p.expanded) {
				return ErrLimit
			}
			if h.Typeflag == tar.TypeDir || !supportedMember(h.Name) {
				continue
			}
			content, err := p.read(tr)
			if err != nil {
				return err
			}
			if err := p.decode(h.Name, content, depth+1); err != nil {
				return err
			}
			matched++
		}
		if matched == 0 {
			return invalid("tar has no browser records")
		}
		p.snapshot.Format = "archive"
		return nil
	}
	if bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		p.snapshot.Format = "sqlite"
		return p.sqlite(data)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		p.snapshot.Format = "html"
		return p.html(data)
	}
	return p.json(data, strings.HasSuffix(lower, ".jsonl"))
}
