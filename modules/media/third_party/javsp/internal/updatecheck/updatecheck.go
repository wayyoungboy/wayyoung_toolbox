package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Status string

const (
	StatusDisabled      Status = "disabled"
	StatusAlreadyLatest Status = "already_latest"
	StatusNewVersion    Status = "new_version"
	StatusFailed        Status = "failed"
)

type Options struct {
	AllowCheck     *bool
	CurrentVersion string
	APIURL         string
	ReleaseURL     string
	Client         *http.Client
}

type Result struct {
	Status         Status
	CurrentVersion string
	LatestVersion  string
	ReleaseURL     string
	ReleaseDate    string
	ChangeLog      []string
	AssetName      string
	AssetURL       string
	Err            error
}

func Check(ctx context.Context, opts Options) (Result, error) {
	result := Result{
		Status:         StatusDisabled,
		CurrentVersion: opts.CurrentVersion,
		ReleaseURL:     valueOr(opts.ReleaseURL, "https://github.com/Yuukiy/javsp-go/releases/latest"),
	}
	if opts.AllowCheck != nil && !*opts.AllowCheck {
		return result, nil
	}
	apiURL := valueOr(opts.APIURL, "https://api.github.com/repos/Yuukiy/javsp-go/releases/latest")
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Status = StatusFailed
		result.Err = err
		return result, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		result.Status = StatusFailed
		result.Err = fmt.Errorf("http %d", resp.StatusCode)
		return result, nil
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		result.Status = StatusFailed
		result.Err = err
		return result, nil
	}
	result.LatestVersion = release.TagName
	if release.HTMLURL != "" {
		result.ReleaseURL = release.HTMLURL
	}
	if len(release.Assets) > 0 {
		result.AssetName = release.Assets[0].Name
		result.AssetURL = release.Assets[0].BrowserDownloadURL
	}
	if release.PublishedAt != "" {
		if t, err := time.Parse(time.RFC3339, release.PublishedAt); err == nil {
			result.ReleaseDate = t.Local().Format("2006-01-02")
		}
	}
	result.ChangeLog = changeLog(release.Body, result.ReleaseDate)
	if compareVersion(opts.CurrentVersion, release.TagName) < 0 {
		result.Status = StatusNewVersion
	} else {
		result.Status = StatusAlreadyLatest
	}
	return result, nil
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func Format(result Result) string {
	var b strings.Builder
	title := "JavSP-Go"
	if result.CurrentVersion != "" {
		title += ": " + result.CurrentVersion
	}
	switch result.Status {
	case StatusNewVersion:
		b.WriteString(title)
		b.WriteString("\n")
		b.WriteString("有新版本可下载: ")
		b.WriteString(result.LatestVersion)
		b.WriteString("\n")
		if result.ReleaseURL != "" {
			b.WriteString(result.ReleaseURL)
			b.WriteString("\n")
		}
		for _, line := range result.ChangeLog {
			b.WriteString(line)
			b.WriteString("\n")
		}
	case StatusAlreadyLatest:
		b.WriteString(title)
		b.WriteString(" (已是最新版)\n")
	case StatusFailed:
		b.WriteString(title)
		b.WriteString("\n检查更新失败")
		if result.ReleaseURL != "" {
			b.WriteString("，请前往以下地址查看最新版本:\n")
			b.WriteString(result.ReleaseURL)
		}
		b.WriteString("\n")
	default:
		b.WriteString(title)
		b.WriteString("\n")
	}
	return b.String()
}

func changeLog(body string, releaseDate string) []string {
	var out []string
	if releaseDate != "" {
		out = append(out, "更新时间: "+releaseDate)
	}
	enableHead := true
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			enableHead = false
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			continue
		}
		if strings.HasPrefix(line, "- ") {
			enableHead = false
			out = append(out, line)
			continue
		}
		if enableHead {
			out = append(out, line)
		}
	}
	return out
}

func compareVersion(left string, right string) int {
	leftParts := versionParts(left)
	rightParts := versionParts(right)
	maxLen := len(leftParts)
	if len(rightParts) > maxLen {
		maxLen = len(rightParts)
	}
	for i := 0; i < maxLen; i++ {
		leftValue := 0
		if i < len(leftParts) {
			leftValue = leftParts[i]
		}
		rightValue := 0
		if i < len(rightParts) {
			rightValue = rightParts[i]
		}
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func versionParts(value string) []int {
	cleaned := strings.TrimPrefix(strings.TrimSpace(value), "v")
	rawParts := strings.Split(cleaned, ".")
	parts := make([]int, 0, len(rawParts))
	for _, part := range rawParts {
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		parts = append(parts, n)
	}
	return parts
}

func valueOr(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
