package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

const (
	defaultUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36"
	defaultAcceptLanguage = "zh-CN,zh;q=0.9,en;q=0.8"
)

var ErrSiteBlocked = errors.New("site blocked")

type Options struct {
	ProxyURL string
	Timeout  time.Duration
	Headers  map[string]string
	Cookies  []*http.Cookie
}

type Client struct {
	httpClient *http.Client
	headers    map[string]string
	cookies    []*http.Cookie
}

func New(opts Options) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if opts.ProxyURL != "" {
		proxyURL, err := url.Parse(opts.ProxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	headers := map[string]string{
		"User-Agent":      defaultUserAgent,
		"Accept-Language": defaultAcceptLanguage,
	}
	for key, value := range opts.Headers {
		headers[http.CanonicalHeaderKey(key)] = value
	}
	return &Client{
		httpClient: &http.Client{Transport: transport, Timeout: timeout},
		headers:    headers,
		cookies:    append([]*http.Cookie(nil), opts.Cookies...),
	}, nil
}

func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

func (c *Client) WithCookies(cookies []*http.Cookie) *Client {
	headers := make(map[string]string, len(c.headers))
	for key, value := range c.headers {
		headers[key] = value
	}
	return &Client{
		httpClient: c.httpClient,
		headers:    headers,
		cookies:    append([]*http.Cookie(nil), cookies...),
	}
}

func (c *Client) Get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	c.decorate(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) PostForm(ctx context.Context, rawURL string, data url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.decorate(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) GetHTML(ctx context.Context, rawURL string) (*html.Node, error) {
	resp, err := c.Get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return htmlquery.Parse(resp.Body)
}

func (c *Client) decorate(req *http.Request) {
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
}

func checkResponse(resp *http.Response) error {
	if resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if strings.Contains(string(body), "Just a moment") {
			return fmt.Errorf("%w: %s", ErrSiteBlocked, resp.Request.URL.String())
		}
		return fmt.Errorf("http status %d: %s", resp.StatusCode, resp.Request.URL.String())
	}
	if resp.StatusCode >= 400 {
		_ = resp.Body.Close()
		return fmt.Errorf("http status %d: %s", resp.StatusCode, resp.Request.URL.String())
	}
	return nil
}
