// Package fnos calls only documented fnOS application APIs through their Unix socket.
package fnos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Client struct {
	token   func() string
	appName string
	http    *http.Client
}

func NewClient(token func() string) *Client {
	// The desktop grants access to the installed host app, not an embedded module.
	appName := os.Getenv("TRIM_APPNAME")
	if appName == "" {
		appName = "backup_center"
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &Client{token: token, appName: appName, http: &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", "/var/run/trim_open_gateway_apiscope.socket")
		},
		MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second,
	}}}
}
func (c *Client) SharedFolders(ctx context.Context) ([]string, error) {
	token := c.token()
	if token == "" {
		return nil, errors.New("系统未注入应用 API 授权，请检查系统版本和应用权限")
	}
	b, _ := json.Marshal(map[string]any{"req": "trim.file.getSharedAccessibleFolders", "appName": c.appName, "data": map[string]any{}})
	r, err := http.NewRequestWithContext(ctx, "POST", "http://localhost/api/v1/trimapp", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(r)
	if err != nil {
		return nil, errors.New("无法连接飞牛目录授权接口")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("飞牛授权接口 HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Paths []string `json:"paths"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, errors.New("飞牛授权接口响应无效")
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("飞牛目录授权查询失败（代码 %d）", envelope.Code)
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, p := range envelope.Data.Paths {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return nil, errors.New("飞牛返回了不安全的目录授权")
		}
		if !seen[p] {
			paths = append(paths, p)
			seen[p] = true
		}
	}
	return paths, nil
}
