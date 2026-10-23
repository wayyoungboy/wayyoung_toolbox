package browserdata

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "br" {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
func (p *parser) html(data []byte) error {
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return invalid("HTML")
	}
	bookmarks := false
	var detect func(*html.Node)
	detect = func(n *html.Node) {
		if n.Type == html.DoctypeNode && strings.EqualFold(n.Data, "netscape-bookmark-file-1") {
			bookmarks = true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			detect(c)
		}
	}
	detect(root)
	if p.kind == "bookmarks" && !bookmarks {
		return invalid("HTML bookmarks")
	}
	if p.kind == "history" && bookmarks {
		return invalid("HTML history")
	}
	matched := false
	var walk func(*html.Node, int) error
	walk = func(n *html.Node, depth int) error {
		if depth > 64 {
			return ErrLimit
		}
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if p.kind == "bookmarks" && n.Type == html.ElementNode && n.Data == "a" {
			matched = true
			when, _ := strconv.ParseFloat(attr(n, "add_date"), 64)
			at := ""
			if when > 0 {
				at = stamp(when, 1, 0)
			}
			return p.add(attr(n, "href"), text(n), at)
		}
		if p.kind == "history" && n.Type == html.ElementNode && n.Data == "div" && strings.Contains(" "+attr(n, "class")+" ", " outer-cell ") {
			matched = true
			var link *html.Node
			var find func(*html.Node)
			find = func(c *html.Node) {
				if link == nil && c.Type == html.ElementNode && c.Data == "a" {
					link = c
				}
				for child := c.FirstChild; child != nil; child = child.NextSibling {
					find(child)
				}
			}
			find(n)
			content := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(text(n)), "\u202f", " "), "\u00a0", " ")
			action := false
			for _, word := range []string{"Visited", "Searched for", "Viewed", "Watched"} {
				if strings.HasPrefix(content, word+" ") {
					action = true
				}
			}
			if !action {
				return nil
			}
			if link == nil {
				p.snapshot.Skipped++
				return nil
			}
			at := ""
			for _, line := range strings.Split(content, "\n") {
				line = strings.TrimSpace(line)
				zone := time.UTC
				if strings.HasSuffix(line, " HKT") {
					zone = time.FixedZone("HKT", 8*3600)
				}
				when, e := time.ParseInLocation("Jan 2, 2006, 3:04:05 PM MST", line, zone)
				if e == nil && (strings.HasSuffix(line, " HKT") || strings.HasSuffix(line, " UTC") || strings.HasSuffix(line, " GMT")) {
					at = when.UTC().Format(time.RFC3339Nano)
					break
				}
			}
			if at == "" {
				return invalid("Takeout timestamp")
			}
			return p.add(attr(link, "href"), text(link), at)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return err
	}
	if !matched && !bookmarks {
		return invalid("HTML activity")
	}
	return nil
}
