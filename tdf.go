package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// TheDraw font types.
const (
	TypeOutline = 0
	TypeBlock   = 1
	TypeColor   = 2
)

var typeNames = [...]string{"outline", "block", "color"}

// Code page 437 -> Unicode, with the graphical glyphs for control codes.
var cp437 = []rune(" ☺☻♥♦♣♠•◘○◙♂♀♪♫☼►◄↕‼¶§▬↨↑↓→←∟↔▲▼" +
	" !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~⌂" +
	"ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐" +
	"└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ ")

// Outline fonts store letters 'A'..'Q' that map onto line-drawing pieces.
var outlineMap = []rune("──││┌┐┌┐└┘└┘┤├   ")

func init() {
	if len(cp437) != 256 {
		panic("cp437 table has wrong length")
	}
}

// Character classes used for filtering. Index order matches Font.Count.
var classNames = [4]string{"A-Z", "a-z", "0-9", "!?"}

func classOf(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return 0
	case c >= 'a' && c <= 'z':
		return 1
	case c >= '0' && c <= '9':
		return 2
	default:
		return 3
	}
}

var classSize [4]int

func init() {
	for c := byte(33); c < 127; c++ {
		classSize[classOf(c)]++
	}
}

type cell struct {
	r rune
	a int // DOS attribute byte, or -1 for terminal default
}

type glyph struct {
	w, h int
	rows [][]cell
}

type Font struct {
	File, Name string
	Index      int // position within its .TDF file
	Type       int
	Spacing    int
	data       []byte
	offs       [94]uint16
	has        [94]bool
	Count      [4]int
	Height     int // tallest glyph, in rows
	cache      map[rune]*glyph
}

func (f *Font) Has(r rune) bool {
	return r > 32 && r < 127 && f.has[r-33]
}

func (f *Font) TypeName() string {
	if f.Type < len(typeNames) {
		return typeNames[f.Type]
	}
	return "?"
}

// LoadDir parses every .TDF file directly inside dir.
func LoadDir(dir string) ([]*Font, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".tdf") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var fonts []*Font
	for _, n := range names {
		d, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		fonts = append(fonts, parseTDF(n, d)...)
	}
	return fonts, nil
}

func parseTDF(file string, d []byte) []*Font {
	if len(d) < 20 || string(d[1:19]) != "TheDraw FONTS file" {
		return nil
	}
	var fonts []*Font
	p := 20
	for p+213 <= len(d) && bytes.Equal(d[p:p+4], []byte{0x55, 0xaa, 0x00, 0xff}) {
		nl := min(int(d[p+4]), 12)
		name := d[p+5 : p+5+nl]
		if i := bytes.IndexByte(name, 0); i >= 0 {
			name = name[:i]
		}
		f := &Font{
			File:    file,
			Index:   len(fonts),
			Name:    strings.TrimSpace(string(name)),
			Type:    int(d[p+21]),
			Spacing: int(d[p+22]),
			cache:   map[rune]*glyph{},
		}
		size := int(binary.LittleEndian.Uint16(d[p+23:]))
		end := min(p+213+size, len(d))
		f.data = d[p+213 : end]
		for i := range 94 {
			o := binary.LittleEndian.Uint16(d[p+25+2*i:])
			f.offs[i] = o
			if o != 0xffff && int(o)+2 <= len(f.data) {
				f.has[i] = true
				f.Count[classOf(byte(33+i))]++
				f.Height = max(f.Height, int(f.data[int(o)+1]))
			}
		}
		fonts = append(fonts, f)
		p = end
	}
	return fonts
}

func blankRow(w int) []cell {
	row := make([]cell, w)
	for i := range row {
		row[i] = cell{' ', -1}
	}
	return row
}

func (f *Font) spaceWidth() int {
	total, n := 0, 0
	for i, ok := range f.has {
		if ok {
			total += int(f.data[f.offs[i]])
			n++
		}
	}
	if n == 0 {
		return 3
	}
	return max(2, total/n/2)
}

// Glyph returns the rendered glyph for r, or nil when the font lacks it.
func (f *Font) Glyph(r rune) *glyph {
	if g, ok := f.cache[r]; ok {
		return g
	}
	var g *glyph
	if r == ' ' {
		w := f.spaceWidth()
		g = &glyph{w: w, h: 1, rows: [][]cell{blankRow(w)}}
	} else if f.Has(r) {
		g = f.decode(int(f.offs[r-33]))
	}
	f.cache[r] = g
	return g
}

func (f *Font) decode(o int) *glyph {
	d := f.data
	w, h := int(d[o]), int(d[o+1])
	rows := [][]cell{nil}
	for i := o + 2; i < len(d); {
		b := d[i]
		i++
		if b == 0 {
			break
		}
		if b == '\r' {
			rows = append(rows, nil)
			continue
		}
		c := cell{cp437[b], -1}
		switch f.Type {
		case TypeColor:
			if i < len(d) {
				c.a = int(d[i])
				i++
			}
		case TypeOutline:
			switch {
			case b >= 'A' && int(b-'A') < len(outlineMap):
				c.r = outlineMap[b-'A']
			case b == '@' || b == '&':
				c.r = ' '
			}
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], c)
	}
	if len(rows) > max(h, 1) && len(rows[len(rows)-1]) == 0 {
		rows = rows[:len(rows)-1]
	}
	for _, r := range rows {
		w = max(w, len(r))
	}
	for len(rows) < h {
		rows = append(rows, nil)
	}
	for i, r := range rows {
		rows[i] = append(r, blankRow(w-len(r))...)
	}
	return &glyph{w: w, h: len(rows), rows: rows}
}

func swapCase(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z':
		return r - 32
	case r >= 'A' && r <= 'Z':
		return r + 32
	}
	return r
}

// Compose places glyphs side by side, separated by the font's spacing.
func (f *Font) Compose(gs []*glyph) [][]cell {
	h, w := 0, 0
	for i, g := range gs {
		h = max(h, g.h)
		if i > 0 {
			w += f.Spacing
		}
		w += g.w
	}
	block := make([][]cell, h)
	for y := range block {
		block[y] = blankRow(w)
	}
	x := 0
	for _, g := range gs {
		for y, row := range g.rows {
			copy(block[y][x:], row)
		}
		x += g.w + f.Spacing
	}
	return block
}

// Width returns the column width of gs laid out by Compose.
func (f *Font) Width(gs []*glyph) int {
	w := 0
	for i, g := range gs {
		if i > 0 {
			w += f.Spacing
		}
		w += g.w
	}
	return w
}

// Render lays out text in the font, wrapping between words.
//
// soft is the preferred line limit (the wrap width): words share a line only
// while it holds. A word wider than soft is broken at punctuation
// (0xfdb.xyz -> 0xfdb / .xyz) where it can be; a piece without break points
// stays whole. hard is the limit a line must never break (IRC bytes): only a
// piece that breaks it is split, into pieces of even width.
// With fallback, a missing letter is replaced by its other case.
// It returns the rendered rows and the characters the font could not draw.
func (f *Font) Render(text string, soft, hard func([]*glyph) bool, fallback bool) ([][]cell, string) {
	var missing []rune
	seen := map[rune]bool{}
	var lines [][]*glyph
	var cur []*glyph
	flush := func() {
		if len(cur) > 0 {
			lines = append(lines, cur)
		}
		cur = nil
	}
	space := f.Glyph(' ')
	for _, word := range strings.Fields(text) {
		var gs []*glyph
		var brk []bool // brk[i]: the word may break before gs[i]
		pending := false
		for _, r := range word {
			g := f.Glyph(r)
			if g == nil && fallback {
				g = f.Glyph(swapCase(r))
			}
			if g == nil {
				if !seen[r] {
					seen[r] = true
					missing = append(missing, r)
				}
				// A missing "." still marks where the word may break.
				pending = pending || isBreak(r)
				continue
			}
			brk = append(brk, len(gs) > 0 && (pending || strings.ContainsRune(breakBefore, r)))
			pending = strings.ContainsRune(breakAfter, r)
			gs = append(gs, g)
		}
		if len(gs) == 0 {
			continue
		}
		if len(cur) > 0 {
			cand := append(append(slices.Clone(cur), space), gs...)
			if soft(cand) && hard(cand) {
				cur = cand
				continue
			}
			flush()
		}
		if soft(gs) && hard(gs) {
			cur = gs
			continue
		}
		// Too wide: pack the word's punctuation-separated parts into lines.
		for _, part := range breakParts(gs, brk) {
			if cand := append(slices.Clone(cur), part...); len(cur) > 0 && soft(cand) && hard(cand) {
				cur = cand
				continue
			}
			flush()
			pieces := f.splitEven(part, hard)
			lines = append(lines, pieces[:len(pieces)-1]...)
			cur = pieces[len(pieces)-1]
		}
	}
	flush()

	var out [][]cell
	for li, l := range lines {
		if li > 0 {
			out = append(out, nil)
		}
		out = append(out, f.Compose(l)...)
	}
	return out, string(missing)
}

// Punctuation a too-wide word may break at: before a character that starts a
// part (".xyz", "@host"), or after one that ends a part ("foo-", "a/").
const (
	breakBefore = ".@#:"
	breakAfter  = "-/_,;!?&"
)

func isBreak(r rune) bool {
	return strings.ContainsRune(breakBefore, r) || strings.ContainsRune(breakAfter, r)
}

// breakParts splits a word's glyphs where brk allows a break.
func breakParts(gs []*glyph, brk []bool) [][]*glyph {
	var parts [][]*glyph
	start := 0
	for i := 1; i < len(gs); i++ {
		if brk[i] {
			parts = append(parts, gs[start:i])
			start = i
		}
	}
	return append(parts, gs[start:])
}

// splitEven splits a word into the fewest pieces that each satisfy hard,
// cutting where the pieces come out closest to equal width.
func (f *Font) splitEven(gs []*glyph, hard func([]*glyph) bool) [][]*glyph {
	if hard(gs) {
		return [][]*glyph{gs}
	}
	// prefix[i] is the width of gs[:i].
	prefix := make([]int, len(gs)+1)
	for i, g := range gs {
		prefix[i+1] = prefix[i] + g.w
	}
	total := prefix[len(gs)]
	for k := 2; k <= len(gs); k++ {
		var pieces [][]*glyph
		start, ok := 0, true
		for p := 1; p < k && ok; p++ {
			// Cut nearest to p/k of the width, leaving room for the remaining pieces.
			target := total * p / k
			cut := start + 1
			for c := start + 1; c <= len(gs)-(k-p); c++ {
				if abs(prefix[c]-target) < abs(prefix[cut]-target) {
					cut = c
				}
			}
			pieces = append(pieces, gs[start:cut])
			ok = hard(gs[start:cut])
			start = cut
		}
		pieces = append(pieces, gs[start:])
		if ok && hard(gs[start:]) {
			return pieces
		}
	}
	// Even single glyphs break the limit; one per line is the best we can do.
	pieces := make([][]*glyph, len(gs))
	for i, g := range gs {
		pieces[i] = []*glyph{g}
	}
	return pieces
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Charset returns the font's defined characters grouped by class.
func (f *Font) Charset() string {
	var groups [4][]byte
	for i, ok := range f.has {
		if ok {
			c := byte(33 + i)
			groups[classOf(c)] = append(groups[classOf(c)], c)
		}
	}
	var parts []string
	for _, g := range groups {
		if len(g) > 0 {
			parts = append(parts, string(g))
		}
	}
	return strings.Join(parts, " ")
}
