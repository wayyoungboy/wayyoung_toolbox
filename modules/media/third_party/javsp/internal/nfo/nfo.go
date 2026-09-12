package nfo

import (
	"bytes"
	"os"
	"strings"

	"github.com/yuukiy/javsp-go/internal/model"
)

type Options struct {
	CustomGenresFields []string
	CustomTagsFields   []string
	CensorText         []string
	DefaultTitle       string
	DefaultActress     string
	DefaultSeries      string
	DefaultDirector    string
	DefaultProducer    string
	DefaultPublisher   string
}

func Write(info *model.MovieInfo, path string, opts Options) error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>` + "\n")
	b.WriteString("<movie>\n")

	title := info.Title
	if info.NFOTitle != "" {
		title = info.NFOTitle
	}
	writeElement(&b, "  ", "title", title)
	if info.OriginalTitle != "" {
		writeElement(&b, "  ", "originaltitle", info.OriginalTitle)
	}
	if info.Score != "" {
		writeElement(&b, "  ", "rating", info.Score)
	}
	if info.Plot != "" {
		writeElement(&b, "  ", "plot", info.Plot)
	}
	if info.Duration != "" {
		writeElement(&b, "  ", "runtime", info.Duration)
	}
	writeElement(&b, "  ", "mpaa", "NC-17")

	if info.DVDID != "" {
		writeElementAttrs(&b, "  ", "uniqueid", map[string]string{"type": "num", "default": "true"}, info.DVDID)
	}
	if info.CID != "" {
		writeElementAttrs(&b, "  ", "uniqueid", map[string]string{"type": "cid"}, info.CID)
	}

	dic := infoDict(info, opts)
	genres := preferredGenres(info)
	for _, pattern := range opts.CustomGenresFields {
		genres = append(genres, formatPattern(pattern, dic))
	}
	for _, genre := range uniqueNonEmpty(genres) {
		writeElement(&b, "  ", "genre", genre)
	}

	var tags []string
	for _, pattern := range opts.CustomTagsFields {
		tags = append(tags, formatPattern(pattern, dic))
	}
	for _, tag := range uniqueNonEmpty(tags) {
		writeElement(&b, "  ", "tag", tag)
	}

	writeElement(&b, "  ", "country", "日本")
	if info.Serial != "" {
		b.WriteString("  <set>\n")
		writeElement(&b, "    ", "name", info.Serial)
		b.WriteString("  </set>\n")
	}
	if info.Director != "" {
		writeElement(&b, "  ", "director", info.Director)
	}
	if info.PublishDate != "" {
		writeElement(&b, "  ", "premiered", info.PublishDate)
	}
	if info.Producer != "" {
		writeElement(&b, "  ", "studio", info.Producer)
	}
	if info.PreviewVideo != "" {
		writeElement(&b, "  ", "trailer", info.PreviewVideo)
	}
	for _, actor := range info.Actress {
		b.WriteString("  <actor>\n")
		writeElement(&b, "    ", "name", actor)
		if info.ActressPics != nil && info.ActressPics[actor] != "" {
			writeElement(&b, "    ", "thumb", info.ActressPics[actor])
		}
		b.WriteString("  </actor>\n")
	}
	b.WriteString("</movie>\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeElement(b *strings.Builder, indent, name, value string) {
	b.WriteString(indent)
	b.WriteString("<")
	b.WriteString(name)
	b.WriteString(">")
	b.WriteString(escape(value))
	b.WriteString("</")
	b.WriteString(name)
	b.WriteString(">\n")
}

func writeElementAttrs(b *strings.Builder, indent, name string, attrs map[string]string, value string) {
	b.WriteString(indent)
	b.WriteString("<")
	b.WriteString(name)
	if attrs["type"] != "" {
		b.WriteString(` type="`)
		b.WriteString(escape(attrs["type"]))
		b.WriteString(`"`)
	}
	if attrs["default"] != "" {
		b.WriteString(` default="`)
		b.WriteString(escape(attrs["default"]))
		b.WriteString(`"`)
	}
	b.WriteString(">")
	b.WriteString(escape(value))
	b.WriteString("</")
	b.WriteString(name)
	b.WriteString(">\n")
}

func escape(value string) string {
	var b bytes.Buffer
	for _, r := range value {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&#34;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func preferredGenres(info *model.MovieInfo) []string {
	if len(info.GenreNorm) > 0 {
		return append([]string(nil), info.GenreNorm...)
	}
	return append([]string(nil), info.Genre...)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func infoDict(info *model.MovieInfo, opts Options) map[string]string {
	num := info.DVDID
	if num == "" {
		num = info.CID
	}
	title := valueOr(info.Title, opts.DefaultTitle)
	rawTitle := info.OriginalTitle
	if rawTitle == "" {
		rawTitle = title
	}
	date := info.PublishDate
	if date == "" {
		date = "0000-00-00"
	}
	year := strings.SplitN(date, "-", 2)[0]
	label := "---"
	if parts := strings.Split(num, "-"); len(parts) > 1 {
		label = parts[0]
	}
	censor := ""
	if len(opts.CensorText) >= 2 {
		index := 0
		if info.Uncensored != nil && *info.Uncensored {
			index = 1
		}
		censor = opts.CensorText[index]
	}
	return map[string]string{
		"num":       num,
		"title":     title,
		"rawtitle":  rawTitle,
		"actress":   valueOr(strings.Join(info.Actress, ","), opts.DefaultActress),
		"score":     valueOr(info.Score, "0"),
		"censor":    censor,
		"serial":    valueOr(info.Serial, opts.DefaultSeries),
		"director":  valueOr(info.Director, opts.DefaultDirector),
		"producer":  valueOr(info.Producer, opts.DefaultProducer),
		"publisher": valueOr(info.Publisher, opts.DefaultPublisher),
		"date":      date,
		"year":      year,
		"label":     label,
		"genre":     strings.Join(preferredGenres(info), ","),
	}
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func formatPattern(pattern string, values map[string]string) string {
	result := pattern
	for key, value := range values {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}
