package backup

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"fnos-backup-center/internal/securefs"
)

const Schema = 2
const MaxManifestBytes = 32 << 20
const MaxEntries = 200000
const CloudNotice = "已完成挂载层写入与校验；rclone 可能仍在写回缓存，云端持久化待确认。"

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Selection struct {
	Source string `json:"source"`
	Target string `json:"target"`
	RepoID string `json:"repo_id"`
	Mount  Mount  `json:"mount"`
}

func (s Selection) Validate(source bool) error {
	if !idPattern.MatchString(s.RepoID) {
		return errors.New("invalid repository ID")
	}
	if !filepath.IsAbs(s.Target) || filepath.Clean(s.Target) != s.Target || s.Target == "/" {
		return errors.New("invalid destination directory")
	}
	if source {
		if !filepath.IsAbs(s.Source) || filepath.Clean(s.Source) != s.Source || s.Source == "/" {
			return errors.New("invalid source directory")
		}
		if Within(s.Source, s.Target) || Within(s.Target, s.Source) {
			return errors.New("源目录与目标目录不能重叠")
		}
	}
	return nil
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func RepositoryPath(id string) string { return path.Join("fnos-backup-center", id) }

type Entry struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	ObjectID string    `json:"object_id"`
	Modified time.Time `json:"modified"`
}
type Manifest struct {
	Schema      int       `json:"schema"`
	ID          string    `json:"id"`
	RepoID      string    `json:"repo_id"`
	Created     time.Time `json:"created"`
	Files       []Entry   `json:"files"`
	Directories []string  `json:"directories"`
	Digest      string    `json:"digest"`
}

func (m Manifest) checksum() string {
	m.Digest = ""
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (m Manifest) Validate(repoID string) error {
	if m.Schema != Schema || !idPattern.MatchString(m.ID) || m.RepoID != repoID || m.Created.IsZero() || m.Digest != m.checksum() {
		return errors.New("备份清单版本、身份或校验不匹配")
	}
	if len(m.Files)+len(m.Directories) > MaxEntries {
		return errors.New("manifest entry limit exceeded")
	}
	seen := map[string]bool{}
	for _, d := range m.Directories {
		if err := securefs.ValidPath(d); err != nil {
			return err
		}
		if _, ok := seen[d]; ok {
			return errors.New("duplicate manifest path")
		}
		seen[d] = true
	}
	for _, f := range m.Files {
		if err := securefs.ValidPath(f.Path); err != nil {
			return err
		}
		if _, ok := seen[f.Path]; ok {
			return errors.New("duplicate manifest path")
		}
		if f.Size < 0 || !hashPattern.MatchString(f.SHA256) {
			return errors.New("invalid file size or object ID")
		}
		if !strings.HasPrefix(f.ObjectID, f.SHA256+".") || !idPattern.MatchString(strings.TrimPrefix(f.ObjectID, f.SHA256+".")) {
			return errors.New("invalid immutable object instance")
		}
		seen[f.Path] = false
	}
	for p := range seen {
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if isDir, ok := seen[parent]; !ok || !isDir {
				return fmt.Errorf("invalid parent directory for %q", p)
			}
		}
	}
	return nil
}
func sortManifests(m []Manifest) {
	sort.Slice(m, func(i, j int) bool {
		if m[i].Created.Equal(m[j].Created) {
			return m[i].ID < m[j].ID
		}
		return m[i].Created.Before(m[j].Created)
	})
}

type Progress struct {
	Phase      string `json:"phase"`
	Current    string `json:"current,omitempty"`
	Files      int    `json:"files"`
	Total      int    `json:"total"`
	Bytes      int64  `json:"bytes"`
	TotalBytes int64  `json:"total_bytes"`
}
type Preview struct {
	New       int   `json:"new"`
	Changed   int   `json:"changed"`
	Unchanged int   `json:"unchanged"`
	Removed   int   `json:"removed"`
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`
}
type Result struct {
	State      string `json:"state"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	Copied     int    `json:"copied"`
	Unchanged  int    `json:"unchanged"`
	Failed     int    `json:"failed"`
	Bytes      int64  `json:"bytes"`
	Notice     string `json:"notice,omitempty"`
}
