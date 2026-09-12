package sites

import (
	"net/url"
	"path"
	"strings"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

func firstText(node *html.Node, expr string) string {
	found := htmlquery.FindOne(node, expr)
	if found == nil {
		return ""
	}
	return strings.TrimSpace(htmlquery.InnerText(found))
}

func firstAttr(node *html.Node, expr string, attr string) string {
	found := htmlquery.FindOne(node, expr)
	if found == nil {
		return ""
	}
	return strings.TrimSpace(htmlquery.SelectAttr(found, attr))
}

func allAttrs(node *html.Node, expr string, attr string) []string {
	found := htmlquery.Find(node, expr)
	out := make([]string, 0, len(found))
	for _, item := range found {
		value := strings.TrimSpace(htmlquery.SelectAttr(item, attr))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func allTexts(node *html.Node, expr string) []string {
	found := htmlquery.Find(node, expr)
	out := make([]string, 0, len(found))
	for _, item := range found {
		value := strings.TrimSpace(htmlquery.InnerText(item))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func absURL(baseURL string, ref string) string {
	if ref == "" {
		return ""
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ref
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(parsed).String()
}

func lastPathSegment(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return path.Base(rawURL)
	}
	return path.Base(parsed.Path)
}

func pathSegmentFromEnd(rawURL string, offset int) string {
	parsed, err := url.Parse(rawURL)
	target := rawURL
	if err == nil {
		target = parsed.Path
	}
	parts := strings.Split(strings.Trim(target, "/"), "/")
	if len(parts) == 0 || offset >= len(parts) {
		return ""
	}
	return parts[len(parts)-1-offset]
}

func tailText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var parts []string
	for sibling := node.NextSibling; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type == html.TextNode {
			value := strings.TrimSpace(sibling.Data)
			if value != "" {
				parts = append(parts, value)
			}
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func cleanURLSlashes(rawURL string) string {
	if len(rawURL) <= 8 {
		return rawURL
	}
	return rawURL[:8] + strings.ReplaceAll(rawURL[8:], "//", "/")
}
