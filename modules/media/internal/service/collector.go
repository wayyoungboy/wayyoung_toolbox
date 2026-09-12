package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	htmlstd "html"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	htmlpkg "golang.org/x/net/html"
)

const (
	maxHTTPBody     = 8 << 20
	maxFeedEntries  = 200
	httpGetAttempts = 3
)

var (
	magnetPattern = regexp.MustCompile(`(?i)magnet:\?[^\s<>"']+`)
	reffPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`(?i)name=["']reff["'][^>]*value=["']([^"']+)`),
		regexp.MustCompile(`(?i)value=["']([^"']+)["'][^>]*name=["']reff["']`),
	}
)

type Collector struct{}

type remoteHTTPError struct {
	status int
}

func (err *remoteHTTPError) Error() string {
	return fmt.Sprintf("远端返回 HTTP %d", err.status)
}

func splitKeywords(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range regexp.MustCompile(`[,，\n]`).Split(value, -1) {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}
	return result
}

func titleMatches(title string, includeAll, includeAny, excludeAny []string) (bool, string) {
	folded := strings.ToLower(title)
	for _, term := range splitKeywords(includeAll) {
		if !strings.Contains(folded, strings.ToLower(term)) {
			return false, "未满足全部必含关键词"
		}
	}
	optional := splitKeywords(includeAny)
	if len(optional) > 0 {
		matched := false
		for _, term := range optional {
			matched = matched || strings.Contains(folded, strings.ToLower(term))
		}
		if !matched {
			return false, "未命中任一关键词"
		}
	}
	for _, term := range splitKeywords(excludeAny) {
		if strings.Contains(folded, strings.ToLower(term)) {
			return false, "命中排除关键词"
		}
	}
	return true, "匹配"
}

func firstMagnet(value string) string {
	magnet := strings.TrimSpace(magnetPattern.FindString(htmlstd.UnescapeString(value)))
	if len(magnet) > 16<<10 {
		return ""
	}
	return magnet
}

func boundedText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return strings.ToValidUTF8(value[:maximum], "") + "…"
}

func extractLinks(markup string) []string {
	document, err := htmlpkgParse(strings.NewReader(markup))
	if err != nil {
		return nil
	}
	links := []string{}
	var walk func(*htmlpkg.Node)
	walk = func(node *htmlpkg.Node) {
		if node.Type == htmlpkg.ElementNode && strings.EqualFold(node.Data, "a") {
			for _, attribute := range node.Attr {
				if strings.EqualFold(attribute.Key, "href") && attribute.Val != "" {
					links = append(links, attribute.Val)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return links
}

// Kept behind a variable so parser failure paths can be tested without network access.
var htmlpkgParse = htmlpkg.Parse

func collectorClient(proxyAddress string, timeout int, verifyTLS bool) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Direct clients must not inherit HTTP_PROXY/HTTPS_PROXY from the service
	// environment. Only resolver requests explicitly opt into a configured
	// task or global proxy.
	transport.Proxy = nil
	if proxyAddress != "" {
		proxyURL, err := url.Parse(proxyAddress)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效：%w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !verifyTLS} //nolint:gosec -- user-controlled qB self-signed certificate option
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport, Timeout: time.Duration(clamp(timeout, 5, 120)) * time.Second, Jar: jar}, nil
}

func readResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, &remoteHTTPError{status: response.StatusCode}
	}
	limited := io.LimitReader(response.Body, maxHTTPBody+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxHTTPBody {
		return nil, fmt.Errorf("远端响应超过 8 MiB 限制")
	}
	return body, nil
}

func retryableHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(response *http.Response, attempt int) time.Duration {
	delay := time.Duration(1<<attempt) * time.Second
	if value := strings.TrimSpace(response.Header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
			delay = time.Duration(seconds) * time.Second
		} else if date, err := http.ParseTime(value); err == nil {
			delay = time.Until(date)
		}
	}
	if delay < 0 {
		return 0
	}
	if delay > 10*time.Second {
		return 10 * time.Second
	}
	return delay
}

func getHTTP(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < httpGetAttempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", "fnOS-Media-Acquisition-Center/"+Version)
		response, err := client.Do(request)
		if err == nil {
			retryable := retryableHTTPStatus(response.StatusCode)
			delay := retryDelay(response, attempt)
			body, readErr := readResponse(response)
			if readErr == nil {
				return body, nil
			}
			lastErr = readErr
			if !retryable {
				return nil, readErr
			}
			if attempt+1 < httpGetAttempts {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}
		lastErr = err
		if attempt+1 < httpGetAttempts {
			delay := time.Duration(1<<attempt) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, lastErr
}

func (Collector) FetchEntries(ctx context.Context, task Task, settings Settings) ([]FeedEntry, error) {
	proxy := ""
	if settings.FeedUseProxy {
		proxy = task.ProxyURL
		if proxy == "" {
			proxy = settings.GlobalProxyURL
		}
	}
	client, err := collectorClient(proxy, settings.RequestTimeout, true)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	body, err := getHTTP(ctx, client, task.FeedURL)
	if err != nil {
		return nil, fmt.Errorf("读取 RSS 失败：%w", err)
	}
	feed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("RSS 解析失败：%w", err)
	}
	items := feed.Items
	if len(items) > maxFeedEntries {
		items = items[:maxFeedEntries]
	}
	entries := make([]FeedEntry, 0, len(items))
	for _, item := range items {
		matched, reason := titleMatches(item.Title, task.IncludeAll, task.IncludeAny, task.ExcludeAny)
		summary := item.Content
		if summary == "" {
			summary = item.Description
		}
		published := item.Published
		if published == "" {
			published = item.Updated
		}
		entries = append(entries, FeedEntry{Title: boundedText(item.Title, 1024), EntryID: boundedText(item.GUID, 4096), SourceURL: boundedText(item.Link, 4096),
			Published: boundedText(published, 512), Summary: boundedText(summary, 1<<20), Matched: matched, Reason: reason})
	}
	return entries, nil
}

func (Collector) ResolveMagnet(ctx context.Context, entry FeedEntry, task Task, settings Settings) (string, error) {
	if direct := firstMagnet(entry.Summary); direct != "" {
		return direct, nil
	}
	if direct := firstMagnet(entry.SourceURL); direct != "" {
		return direct, nil
	}
	if task.Extractor != "rmdown" {
		return "", fmt.Errorf("条目中未找到磁力链接")
	}
	var detailLink string
	for _, link := range extractLinks(entry.Summary) {
		if strings.Contains(strings.ToLower(link), "hash") {
			detailLink = link
			break
		}
	}
	if err := validateHTTPURL(detailLink, "详情链接"); err != nil {
		return "", fmt.Errorf("摘要中未找到有效的 hash 详情链接")
	}
	proxy := task.ProxyURL
	if proxy == "" {
		proxy = settings.GlobalProxyURL
	}
	client, err := collectorClient(proxy, settings.RequestTimeout, true)
	if err != nil {
		return "", err
	}
	defer client.CloseIdleConnections()
	get := func(address string) ([]byte, error) {
		return getHTTP(ctx, client, address)
	}
	detail, err := get(detailLink)
	if err != nil {
		return "", fmt.Errorf("读取详情页失败：%w", err)
	}
	var reff string
	for _, pattern := range reffPatterns {
		match := pattern.FindSubmatch(detail)
		if len(match) > 1 {
			reff = string(match[1])
			break
		}
	}
	if reff == "" {
		return "", fmt.Errorf("详情页未找到 reff 参数")
	}
	parsed, _ := url.Parse(detailLink)
	ref := parsed.Query().Get("hash")
	if ref == "" {
		ref = parsed.Query().Get("ref")
	}
	if ref == "" {
		return "", fmt.Errorf("详情链接未包含 hash/ref 参数")
	}
	endpoint := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/download.php"}
	query := endpoint.Query()
	query.Set("action", "magnet")
	query.Set("ref", ref)
	query.Set("reff", reff)
	endpoint.RawQuery = query.Encode()
	magnetBody, err := get(endpoint.String())
	if err != nil {
		return "", fmt.Errorf("读取磁力链接失败：%w", err)
	}
	magnet := firstMagnet(string(magnetBody))
	if magnet == "" {
		return "", fmt.Errorf("下载接口未返回磁力链接")
	}
	return magnet, nil
}

func (collector Collector) Preview(ctx context.Context, task Task, settings Settings) (map[string]any, error) {
	entries, err := collector.FetchEntries(ctx, task, settings)
	if err != nil {
		return nil, err
	}
	limit := clamp(len(entries), 0, 10)
	items := make([]FeedEntry, 0, limit)
	matched := 0
	for index, entry := range entries {
		if entry.Matched {
			matched++
		}
		if index >= limit {
			continue
		}
		if entry.Matched {
			magnet, resolveErr := collector.ResolveMagnet(ctx, entry, task, settings)
			entry.MagnetFound = resolveErr == nil
			if resolveErr != nil {
				entry.Error = resolveErr.Error()
			} else {
				entry.Fingerprint = fingerprint(magnet)
			}
		}
		entry.Summary = ""
		items = append(items, entry)
	}
	return map[string]any{"feed_title": task.Name, "entries": items, "total": len(entries), "matched": matched}, nil
}

func qbBaseURL(settings Settings) (*url.URL, error) {
	if strings.TrimSpace(settings.QBHost) == "" {
		return nil, fmt.Errorf("请先配置 qBittorrent 地址")
	}
	parsed, err := url.Parse(settings.QBHost)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("qBittorrent 地址必须是有效的 HTTP(S) 地址")
	}
	if parsed.Port() == "" && settings.QBPort > 0 {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(settings.QBPort))
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func qbRequest(ctx context.Context, client *http.Client, base *url.URL, method, path string, body io.Reader, contentType string) ([]byte, error) {
	reference, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + reference.Path
	target.RawQuery = reference.RawQuery
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Referer", base.String())
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	return readResponse(response)
}

func qbLogin(ctx context.Context, settings Settings) (*http.Client, *url.URL, error) {
	base, err := qbBaseURL(settings)
	if err != nil {
		return nil, nil, err
	}
	client, err := collectorClient("", settings.RequestTimeout, settings.QBVerifyCertificate)
	if err != nil {
		return nil, nil, err
	}
	form := url.Values{"username": {settings.QBUsername}, "password": {settings.QBPassword}}
	body, err := qbRequest(ctx, client, base, http.MethodPost, "/api/v2/auth/login", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, nil, fmt.Errorf("qBittorrent 登录失败：%w", err)
	}
	if strings.TrimSpace(string(body)) != "Ok." {
		return nil, nil, fmt.Errorf("qBittorrent 登录失败：%s", boundedText(strings.TrimSpace(string(body)), 1024))
	}
	return client, base, nil
}

type QBSession struct {
	client *http.Client
	base   *url.URL
}

func (Collector) OpenQBSession(ctx context.Context, settings Settings) (*QBSession, error) {
	client, base, err := qbLogin(ctx, settings)
	if err != nil {
		return nil, err
	}
	return &QBSession{client: client, base: base}, nil
}

func (session *QBSession) Close() { session.client.CloseIdleConnections() }

func magnetInfoHash(magnet string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(magnet))
	if err != nil || !strings.EqualFold(parsed.Scheme, "magnet") {
		return "", false
	}
	const prefix = "urn:btih:"
	for _, exactTopic := range parsed.Query()["xt"] {
		if len(exactTopic) <= len(prefix) || !strings.EqualFold(exactTopic[:len(prefix)], prefix) {
			continue
		}
		rawHash := exactTopic[len(prefix):]
		switch len(rawHash) {
		case 40:
			decoded, decodeErr := hex.DecodeString(rawHash)
			if decodeErr == nil && len(decoded) == 20 {
				return strings.ToLower(rawHash), true
			}
		case 32:
			decoded, decodeErr := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(rawHash))
			if decodeErr == nil && len(decoded) == 20 {
				return hex.EncodeToString(decoded), true
			}
		}
	}
	return "", false
}

func (session *QBSession) HasTorrent(ctx context.Context, magnet string) (bool, error) {
	infoHash, ok := magnetInfoHash(magnet)
	if !ok {
		return false, nil
	}
	path := "/api/v2/torrents/info?hashes=" + url.QueryEscape(infoHash)
	body, err := qbRequest(ctx, session.client, session.base, http.MethodGet, path, nil, "")
	if err != nil {
		return false, err
	}
	var torrents []struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(body, &torrents); err != nil {
		return false, fmt.Errorf("qBittorrent 任务查询响应无效：%w", err)
	}
	for _, torrent := range torrents {
		if strings.EqualFold(torrent.Hash, infoHash) {
			return true, nil
		}
	}
	return false, nil
}

func (session *QBSession) Submit(ctx context.Context, magnet string, task Task) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("urls", magnet)
	if task.QBCategory != "" {
		_ = writer.WriteField("category", task.QBCategory)
	}
	if task.SavePath != "" {
		_ = writer.WriteField("savepath", task.SavePath)
	}
	if err := writer.Close(); err != nil {
		return err
	}
	response, err := qbRequest(ctx, session.client, session.base, http.MethodPost, "/api/v2/torrents/add", &body, writer.FormDataContentType())
	if err != nil {
		return err
	}
	if value := strings.TrimSpace(string(response)); value == "Fails." || value == "Fails" {
		return errQBSubmissionRejected
	} else if value != "Ok." && value != "Ok" {
		return fmt.Errorf("qBittorrent 返回异常：%s", boundedText(value, 1024))
	}
	return nil
}

func (session *QBSession) Torrents(ctx context.Context) ([]QBTorrent, error) {
	body, err := qbRequest(ctx, session.client, session.base, http.MethodGet, "/api/v2/torrents/info", nil, "")
	if err != nil {
		return nil, err
	}
	var torrents []QBTorrent
	if err := json.Unmarshal(body, &torrents); err != nil {
		return nil, fmt.Errorf("qBittorrent 任务列表响应无效：%w", err)
	}
	if torrents == nil {
		return nil, fmt.Errorf("qBittorrent 任务列表响应无效：需要数组，不能为 null")
	}
	return torrents, nil
}

func (session *QBSession) torrentAction(ctx context.Context, path, hash string, values url.Values) error {
	if values == nil {
		values = url.Values{}
	}
	values.Set("hashes", hash)
	body, err := qbRequest(ctx, session.client, session.base, http.MethodPost, path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if value := strings.TrimSpace(string(body)); value != "" && value != "Ok." && value != "Ok" {
		return fmt.Errorf("qBittorrent 返回异常：%s", boundedText(value, 1024))
	}
	return nil
}

func (session *QBSession) StopTorrent(ctx context.Context, hash string) error {
	return session.torrentActionWithLegacyFallback(ctx, "/api/v2/torrents/stop", "/api/v2/torrents/pause", hash)
}

func (session *QBSession) StartTorrent(ctx context.Context, hash string) error {
	return session.torrentActionWithLegacyFallback(ctx, "/api/v2/torrents/start", "/api/v2/torrents/resume", hash)
}

func (session *QBSession) torrentActionWithLegacyFallback(ctx context.Context, currentPath, legacyPath, hash string) error {
	err := session.torrentAction(ctx, currentPath, hash, nil)
	if err == nil {
		return nil
	}
	var statusErr *remoteHTTPError
	if !errors.As(err, &statusErr) || statusErr.status != http.StatusNotFound {
		return err
	}
	return session.torrentAction(ctx, legacyPath, hash, nil)
}

func (session *QBSession) DeleteTorrentRecord(ctx context.Context, hash string) error {
	return session.torrentAction(ctx, "/api/v2/torrents/delete", hash, url.Values{"deleteFiles": {"false"}})
}

func (Collector) TestQBittorrent(ctx context.Context, settings Settings) (map[string]any, error) {
	client, base, err := qbLogin(ctx, settings)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	body, err := qbRequest(ctx, client, base, http.MethodGet, "/api/v2/app/version", nil, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "version": strings.TrimSpace(string(body))}, nil
}

func (collector Collector) Submit(ctx context.Context, magnet string, task Task, settings Settings) error {
	session, err := collector.OpenQBSession(ctx, settings)
	if err != nil {
		return err
	}
	defer session.Close()
	return session.Submit(ctx, magnet, task)
}
