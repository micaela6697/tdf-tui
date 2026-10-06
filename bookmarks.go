package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// BookmarkFile is stored inside the font directory, so each collection
// keeps its own bookmarks.
const BookmarkFile = "tdfbrowse-bookmarks.txt"

// Bookmarks is a set of fonts keyed by file name and position in that file
// (a .TDF can hold several fonts, sometimes with the same name).
type Bookmarks struct {
	path string
	set  map[string]string // key -> font name, kept for readability
}

func bookmarkKey(f *Font) string {
	return f.File + "\t" + strconv.Itoa(f.Index)
}

// LoadBookmarks reads dir's bookmark file; a missing file is an empty set.
func LoadBookmarks(dir string) (*Bookmarks, error) {
	b := &Bookmarks{path: filepath.Join(dir, BookmarkFile), set: map[string]string{}}
	fh, err := os.Open(b.path)
	if os.IsNotExist(err) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		name := ""
		if len(parts) == 3 {
			name = parts[2]
		}
		b.set[parts[0]+"\t"+parts[1]] = name
	}
	return b, sc.Err()
}

func (b *Bookmarks) Has(f *Font) bool {
	_, ok := b.set[bookmarkKey(f)]
	return ok
}

// Toggle flips f's bookmark and saves the file.
func (b *Bookmarks) Toggle(f *Font) error {
	k := bookmarkKey(f)
	if _, ok := b.set[k]; ok {
		delete(b.set, k)
	} else {
		b.set[k] = f.Name
	}
	return b.save()
}

// save writes the set atomically (temp file + rename).
func (b *Bookmarks) save() error {
	keys := make([]string, 0, len(b.set))
	for k := range b.set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("# tdfbrowse bookmarks: file<TAB>font index in file<TAB>font name\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s\t%s\n", k, b.set[k])
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}
