// nfo-translate repairs existing local NFO text only. It never scrapes, renames
// videos, or changes a media library database. Dry run is the default.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/translate"
)

type document struct {
	XMLName xml.Name `xml:"movie"`
	Title   string   `xml:"title"`
	Plot    string   `xml:"plot"`
	Actors  []string `xml:"actor>name"`
}
type span struct {
	name       string
	start, end int
}
type record struct {
	Path   string `json:"path"`
	Fields int    `json:"fields"`
	Before string `json:"before_sha256"`
	After  string `json:"after_sha256"`
	Backup string `json:"backup,omitempty"`
}

func japanese(text string, names []string) bool {
	for _, name := range names {
		if name != "" {
			text = strings.ReplaceAll(text, name, "")
		}
	}
	return strings.ContainsFunc(text, func(r rune) bool { return unicode.In(r, unicode.Hiragana, unicode.Katakana) })
}

// Keep every unmodified byte, including comments, unknown fields and artwork.
func translatedNFO(ctx context.Context, data []byte, engine translate.Engine) ([]byte, int, error) {
	var doc document
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, 0, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	var spans []span
	seen := map[string]bool{}
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 && (token.Name.Local == "title" || token.Name.Local == "plot") {
				if seen[token.Name.Local] {
					return nil, 0, fmt.Errorf("duplicate metadata field")
				}
				seen[token.Name.Local] = true
				spans = append(spans, span{name: token.Name.Local, start: int(decoder.InputOffset())})
			}
		case xml.EndElement:
			if depth == 2 && (token.Name.Local == "title" || token.Name.Local == "plot") {
				spans[len(spans)-1].end = before
			}
			depth--
		}
	}
	fields := translate.Fields{Title: japanese(doc.Title, doc.Actors), Plot: japanese(doc.Plot, doc.Actors)}
	count := 0
	if fields.Title {
		count++
	}
	if fields.Plot {
		count++
	}
	if count == 0 || engine == nil {
		return data, count, nil
	}
	info := &model.MovieInfo{Title: doc.Title, Plot: doc.Plot, Actress: doc.Actors}
	if _, err := translate.Apply(ctx, info, engine, fields); err != nil {
		return nil, 0, err
	}
	// Keep the catalogue prefix intact when the source title has one.
	if first, _, ok := strings.Cut(doc.Title, " "); fields.Title && ok && strings.Contains(first, "-") && !strings.HasPrefix(info.Title, first+" ") {
		return nil, 0, fmt.Errorf("provider changed catalogue prefix")
	}
	out := append([]byte(nil), data...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	for _, s := range spans {
		value := info.Title
		if s.name == "plot" {
			value = info.Plot
		}
		if (s.name == "title" && !fields.Title) || (s.name == "plot" && !fields.Plot) {
			continue
		}
		var escaped bytes.Buffer
		if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
			return nil, 0, err
		}
		out = append(append(append([]byte(nil), out[:s.start]...), escaped.Bytes()...), out[s.end:]...)
	}
	var checked document
	if err := xml.Unmarshal(out, &checked); err != nil {
		return nil, 0, err
	}
	return out, count, nil
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func repairFile(ctx context.Context, root, backup *os.Root, path string, engine translate.Engine, apply bool) (record, error) {
	r := record{Path: path}
	info, err := root.Lstat(path)
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return r, fmt.Errorf("not a regular bounded NFO")
	}
	data, err := root.ReadFile(path)
	if err != nil {
		return r, err
	}
	r.Before = digest(data)
	var translator translate.Engine
	if apply {
		translator = engine
	}
	updated, count, err := translatedNFO(ctx, data, translator)
	if err != nil {
		return r, err
	}
	r.Fields, r.After = count, digest(updated)
	if count == 0 || !apply {
		return r, nil
	}
	if engine == nil || backup == nil {
		return r, fmt.Errorf("apply requires engine and backup")
	}
	r.Backup = digest([]byte(path)) + ".nfo"
	f, err := backup.OpenFile(r.Backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return r, err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return r, err
	}
	tmp := filepath.Join(filepath.Dir(path), ".nfo-translation-"+rand.Text())
	f, err = root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return r, err
	}
	defer root.Remove(tmp)
	_, writeErr = f.Write(updated)
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		err = f.Chown(int(stat.Uid), int(stat.Gid))
	}
	modeErr := f.Chmod(info.Mode().Perm())
	syncErr = f.Sync()
	closeErr = f.Close()
	if err := errors.Join(err, writeErr, modeErr, syncErr, closeErr); err != nil {
		return r, err
	}
	current, err := root.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return r, fmt.Errorf("NFO identity changed before commit")
	}
	currentData, err := root.ReadFile(path)
	if err != nil || !bytes.Equal(data, currentData) {
		return r, fmt.Errorf("NFO content changed before commit")
	}
	if err := root.Rename(tmp, path); err != nil {
		return r, err
	}
	return r, nil
}

func run() error {
	rootPath := flag.String("root", "", "explicit completed-library directory")
	backupPath := flag.String("backup", "", "new private backup directory outside library")
	apply := flag.Bool("apply", false, "write translated metadata (default: read-only)")
	flag.Parse()
	if !filepath.IsAbs(*rootPath) || filepath.Clean(*rootPath) == "/" {
		return fmt.Errorf("explicit library root required")
	}
	root, err := os.OpenRoot(*rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	var backup *os.Root
	var engine translate.Engine
	var log *os.File
	if *apply {
		var cfg struct {
			APIKey string `json:"api_key"`
			Proxy  string `json:"proxy"`
		}
		if err := json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&cfg); err != nil {
			return err
		}
		if cfg.APIKey == "" {
			return fmt.Errorf("missing DeepL key on stdin")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		if cfg.Proxy != "" {
			u, err := url.Parse(cfg.Proxy)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("invalid proxy")
			}
			transport.Proxy = http.ProxyURL(u)
		}
		client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("translation redirects disabled") }}
		engine = translate.DeepLEngine{APIKey: cfg.APIKey, Client: client}
		resolvedRoot, err := filepath.EvalSymlinks(*rootPath)
		if err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(*backupPath))
		if err != nil {
			return err
		}
		resolvedBackup := filepath.Join(parent, filepath.Base(*backupPath))
		rel, err := filepath.Rel(resolvedRoot, resolvedBackup)
		if !filepath.IsAbs(*backupPath) || err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return fmt.Errorf("backup must be outside library")
		}
		if err := os.Mkdir(resolvedBackup, 0700); err != nil {
			return err
		}
		backup, err = os.OpenRoot(resolvedBackup)
		if err != nil {
			return err
		}
		defer backup.Close()
		log, err = backup.OpenFile("manifest.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer log.Close()
	}
	files, affected, fields := 0, 0, 0
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(path), ".nfo") {
			return nil
		}
		files++
		r, err := repairFile(context.Background(), root, backup, path, engine, *apply)
		if err != nil {
			return fmt.Errorf("NFO %s: %w", filepath.Base(path), err)
		}
		if r.Fields == 0 {
			return nil
		}
		affected++
		fields += r.Fields
		if log != nil {
			if err := json.NewEncoder(log).Encode(r); err != nil {
				return err
			}
			if err := log.Sync(); err != nil {
				return err
			}
		}
		fmt.Printf("NFO %s fields=%d applied=%t\n", filepath.Base(path), r.Fields, *apply)
		return nil
	})
	fmt.Printf("scanned=%d affected=%d fields=%d applied=%t\n", files, affected, fields, *apply)
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
