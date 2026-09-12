package proxyfree

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
)

func Get(ctx context.Context, client *http.Client, site string, preferURL string) string {
	if preferURL != "" && ChooseConnectable(ctx, client, []string{preferURL}) != "" {
		return preferURL
	}
	candidates := Candidates(ctx, client, site)
	return ChooseConnectable(ctx, client, candidates)
}

func Candidates(ctx context.Context, client *http.Client, site string) []string {
	switch strings.ToLower(site) {
	case "avsox":
		return parseAVSoxURLs(fetchText(ctx, client, "https://tellme.pw/avsox"))
	case "javbus":
		return parseJavBusURLs(fetchText(ctx, client, "https://www.javbus.one/"))
	case "javlib":
		return parseJavLibURLs(fetchText(ctx, client, "https://github.com/javlibcom"))
	case "javdb":
		const baseURL = "https://jav524.app"
		index := fetchText(ctx, client, baseURL)
		for _, script := range parseScriptURLs(index, baseURL) {
			if strings.Contains(script, "/js/index") {
				return parseJavDBJSURLs(fetchText(ctx, client, script))
			}
		}
	}
	return nil
}

func ChooseConnectable(ctx context.Context, client *http.Client, urls []string) string {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	for _, rawURL := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return rawURL
		}
	}
	return ""
}

func parseAVSoxURLs(html string) []string {
	doc, err := htmlquery.Parse(strings.NewReader(html))
	if err != nil {
		return nil
	}
	var out []string
	for _, node := range htmlquery.Find(doc, "//h4/strong/a") {
		if href := strings.TrimSpace(htmlquery.SelectAttr(node, "href")); href != "" {
			out = append(out, href)
		}
	}
	return out
}

func parseJavBusURLs(html string) []string {
	matches := regexp.MustCompile(`(?i)防屏蔽地址：(https://(?:[\d\w][-\d\w]{1,61}[\d\w]\.){1,2}[a-z]{2,})`).FindAllStringSubmatch(html, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match[1])
	}
	return out
}

func parseJavLibURLs(html string) []string {
	doc, err := htmlquery.Parse(strings.NewReader(html))
	if err != nil {
		return nil
	}
	bio := htmlquery.FindOne(doc, "//div[@class='p-note user-profile-bio mb-3 js-user-profile-bio f4']")
	if bio == nil {
		return nil
	}
	text := strings.TrimSpace(htmlquery.InnerText(bio))
	match := regexp.MustCompile(`[\w.]+`).FindString(text)
	if match == "" {
		return nil
	}
	return []string{"https://www." + match + ".com"}
}

func parseJavDBJSURLs(js string) []string {
	match := regexp.MustCompile(`(?i)\$officialUrl\s*=\s*"(https://(?:[\d\w][-\d\w]{1,61}[\d\w]\.){1,2}[a-z]{2,})"`).FindStringSubmatch(js)
	if len(match) < 2 {
		return nil
	}
	return []string{match[1]}
}

func parseScriptURLs(html string, baseURL string) []string {
	doc, err := htmlquery.Parse(strings.NewReader(html))
	if err != nil {
		return nil
	}
	var out []string
	for _, node := range htmlquery.Find(doc, "//script[@src]") {
		if src := htmlquery.SelectAttr(node, "src"); src != "" {
			out = append(out, resolveURL(baseURL, src))
		}
	}
	return out
}

func resolveURL(baseURL string, ref string) string {
	parsedRef, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || parsedRef.IsAbs() {
		return ref
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return ref
	}
	return parsedBase.ResolveReference(parsedRef).String()
}

func fetchText(ctx context.Context, client *http.Client, rawURL string) string {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(data)
}
