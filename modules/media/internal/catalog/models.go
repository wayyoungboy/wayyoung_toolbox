// Package catalog owns the persistent personal media library. Scanning only
// reads media; importing a record never renames or rewrites its source files.
package catalog

import (
	"errors"
	"os"
	"time"
)

var (
	ErrInvalid       = errors.New("馆藏请求无效")
	ErrNotFound      = errors.New("馆藏记录不存在")
	ErrConflict      = errors.New("记录或媒体文件已变化，请刷新后重试")
	ErrUnavailable   = errors.New("媒体目录或文件暂不可访问")
	ErrBusy          = errors.New("馆藏扫描正在运行")
	ErrDeletePending = errors.New("未完成的馆藏删除需要检查")
)

type Library struct {
	RootIdentity string `json:"-"`
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Root         string `json:"root"`
	CreatedAt    string `json:"created_at"`
	ScannedAt    string `json:"scanned_at"`
	ScanError    string `json:"scan_error"`
	Assets       int    `json:"asset_count"`
}

type Progress struct {
	SessionID string  `json:"session_id,omitempty"`
	Position  float64 `json:"position"`
	Duration  float64 `json:"duration"`
	Revision  int64   `json:"revision"`
	PlayedAt  string  `json:"played_at"`
	Completed bool    `json:"completed"`
}

type Subtitle struct {
	Index    int    `json:"index"`
	Label    string `json:"label"`
	Language string `json:"language"`
	Format   string `json:"format"`
	Path     string `json:"path"`
}

type Asset struct {
	ID        string     `json:"id"`
	LibraryID int64      `json:"library_id"`
	WorkID    string     `json:"work_id"`
	Path      string     `json:"path"`
	Size      int64      `json:"size"`
	Available bool       `json:"available"`
	NFOPath   string     `json:"nfo_path"`
	Poster    bool       `json:"poster"`
	Issues    []string   `json:"issues"`
	Subtitles []Subtitle `json:"subtitles"`
	Progress  Progress   `json:"progress"`
}

type Work struct {
	Watched        bool     `json:"watched"`
	Rating         int      `json:"rating"`
	Feedback       string   `json:"feedback"`
	NotesRevision  int64    `json:"notes_revision"`
	ActorProfiles  []Actor  `json:"actor_profiles,omitempty"`
	ID             string   `json:"id"`
	Code           string   `json:"code"`
	Title          string   `json:"title"`
	OriginalTitle  string   `json:"original_title"`
	Plot           string   `json:"plot"`
	Studio         string   `json:"studio"`
	ReleaseDate    string   `json:"release_date"`
	Year           int      `json:"year"`
	Actors         []string `json:"actors"`
	Genres         []string `json:"genres"`
	MetadataSource string   `json:"metadata_source"`
	TitleLocked    bool     `json:"title_locked"`
	AssetCount     int      `json:"asset_count"`
	AvailableCount int      `json:"available_count"`
	CoverAssetID   string   `json:"cover_asset_id"`
	Favorite       bool     `json:"favorite"`
	WatchLater     bool     `json:"watch_later"`
	LastPlayedAt   string   `json:"last_played_at"`
	CreatedAt      string   `json:"created_at"`
	Assets         []Asset  `json:"assets,omitempty"`
}

type Filter struct {
	Watched    *bool
	ActorID    string
	ListID     string
	WatchLater bool
	Sort       string
	Actor      string
	Tag        string
	Query      string
	LibraryID  int64
	Favorite   bool
	Continue   bool
	Limit      int
	Offset     int
}

type Page struct {
	Items  []Work `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type ScanResult struct {
	LibraryID int64  `json:"library_id"`
	ScannedAt string `json:"scanned_at"`
	Assets    int    `json:"asset_count"`
	Works     int    `json:"work_count"`
	Issues    int    `json:"issue_count"`
}

type PersonalPatch struct {
	Watched       *bool   `json:"watched"`
	Rating        *int    `json:"rating"`
	Feedback      *string `json:"feedback"`
	NotesRevision int64   `json:"notes_revision"`
	Favorite      *bool   `json:"favorite"`
	WatchLater    *bool   `json:"watch_later"`
}

type OpenedFile struct {
	File     *os.File
	Name     string
	Size     int64
	Modified time.Time
	MIME     string
}
