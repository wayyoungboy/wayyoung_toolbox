package browserdata

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func attribute(token html.Token, key string) string {
	for _, a := range token.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func (p *parser) activity(content, href, title string) error {
	content = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(content), "\u202f", " "), "\u00a0", " ")
	action := false
	for _, word := range []string{"Visited", "Searched for", "Viewed", "Watched"} {
		if strings.HasPrefix(content, word+" ") {
			action = true
		}
	}
	if !action {
		return nil
	}
	if href == "" {
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
		when, err := time.ParseInLocation("Jan 2, 2006, 3:04:05 PM MST", line, zone)
		if err == nil && (strings.HasSuffix(line, " HKT") || strings.HasSuffix(line, " UTC") || strings.HasSuffix(line, " GMT")) {
			at = when.UTC().Format(time.RFC3339Nano)
			break
		}
	}
	if at == "" {
		return invalid("Takeout timestamp")
	}
	return p.add(href, title, at)
}

// Tokenize instead of allocating a DOM from hostile uploads. Caps apply while
// reading, before any tree/text accumulation, with cancellation on every token.
func (p *parser) html(data []byte) error {
	z := html.NewTokenizer(contextReader{p.ctx, bytes.NewReader(data)})
	z.SetMaxBuf(64 << 10)
	bookmarks, matched := false, false
	divDepth, outerDepth := 0, 0
	inAnchor := false
	href, at := "", ""
	var content, title strings.Builder
	for count := 0; ; count++ {
		if count >= 200000 {
			return ErrLimit
		}
		if err := p.ctx.Err(); err != nil {
			return err
		}
		typ := z.Next()
		if typ == html.ErrorToken {
			if z.Err() != io.EOF {
				return invalid("HTML token")
			}
			break
		}
		token := z.Token()
		switch typ {
		case html.DoctypeToken:
			if fields := strings.Fields(token.Data); len(fields) > 0 && strings.EqualFold(fields[0], "netscape-bookmark-file-1") {
				bookmarks = true
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			if token.Data == "div" {
				divDepth++
				if divDepth > 64 {
					return ErrLimit
				}
				if p.kind == "history" && outerDepth == 0 {
					for _, class := range strings.Fields(attribute(token, "class")) {
						if class == "outer-cell" {
							outerDepth = divDepth
							matched = true
							content.Reset()
							title.Reset()
							href = ""
							inAnchor = false
						}
					}
				}
			}
			if token.Data == "br" && outerDepth > 0 {
				content.WriteByte('\n')
			}
			if token.Data == "a" {
				if p.kind == "bookmarks" {
					matched = true
					inAnchor = true
					href = attribute(token, "href")
					title.Reset()
					when, _ := strconv.ParseFloat(attribute(token, "add_date"), 64)
					at = ""
					if when > 0 {
						at = stamp(when, 1, 0)
					}
				} else if outerDepth > 0 && href == "" {
					href = attribute(token, "href")
					title.Reset()
					inAnchor = true
				}
			}
		case html.TextToken:
			if outerDepth > 0 {
				content.WriteString(token.Data)
			}
			if inAnchor {
				title.WriteString(token.Data)
			}
			if content.Len() > 64<<10 || title.Len() > maxText {
				return ErrLimit
			}
		case html.EndTagToken:
			if token.Data == "a" && inAnchor {
				inAnchor = false
				if p.kind == "bookmarks" {
					if err := p.add(href, title.String(), at); err != nil {
						return err
					}
					href = ""
				}
			}
			if token.Data == "div" {
				if outerDepth > 0 && divDepth == outerDepth {
					if err := p.activity(content.String(), href, title.String()); err != nil {
						return err
					}
					outerDepth = 0
					content.Reset()
					title.Reset()
					href = ""
					inAnchor = false
				}
				if divDepth > 0 {
					divDepth--
				}
			}
		}
	}
	if p.kind == "bookmarks" && !bookmarks || p.kind == "history" && (bookmarks || !matched) || outerDepth > 0 || inAnchor {
		return invalid("HTML browser records")
	}
	return nil
}
