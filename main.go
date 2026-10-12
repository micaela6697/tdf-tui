// tdfbrowse: preview and filter TheDraw (.TDF) fonts in the terminal.
//
// Usage: tdfbrowse [flags] [font-dir]   (default: current directory)
//
// With -irc, Enter prints the preview as mIRC-colored lines on stdout and
// exits; the screen itself is drawn on /dev/tty, so stdout can be captured.
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// DOS color index -> ANSI palette index.
var dosToANSI = [8]int{0, 4, 2, 6, 1, 5, 3, 7}

var attrStyles [256]tcell.Style

func init() {
	for a := range 256 {
		fg, bg := a&15, a>>4&15 // high bg bit treated as iCE bright background
		attrStyles[a] = tcell.StyleDefault.
			Foreground(tcell.PaletteColor(dosToANSI[fg&7] + fg&8)).
			Background(tcell.PaletteColor(dosToANSI[bg&7] + bg&8))
	}
}

func cellStyle(a int) tcell.Style {
	if a < 0 {
		return tcell.StyleDefault
	}
	return attrStyles[a&255]
}

func dosColor(c int) tcell.Color {
	return tcell.PaletteColor(dosToANSI[c&7] + c&8)
}

// noBGLook draws a cell with black shown as the terminal's own background
// (many themes paint palette "black" as a gray). Other colors are untouched.
func noBGLook(c cell) (rune, tcell.Style) {
	r, fg, bg := noBGCell(c)
	st := tcell.StyleDefault
	if fg >= 0 {
		st = st.Foreground(dosColor(fg))
	}
	if bg >= 0 {
		st = st.Background(dosColor(bg))
	}
	return r, st
}

// Per-class requirement, cycled with keys 1-4.
const (
	reqOff = iota
	reqAll
	reqSome
)

var reqLabels = [...]string{"off", "all", "some"}

var sortLabels = [...]string{"name", "height↑", "height↓"}

var typeFilters = []struct {
	label string
	typ   int
}{{"all", -1}, {"color", TypeColor}, {"block", TypeBlock}, {"outline", TypeOutline}}

const (
	editNone = iota
	editName
	editGlyphs
	editText
	editWidth
)

type App struct {
	s     tcell.Screen
	all   []*Font
	shown []*Font

	sel, top, pscroll int
	require           [4]int // reqOff, reqAll or reqSome per character class
	typeFilter        int
	nameQ, glyphQ     string
	text              string
	fileW             int
	sortMode          int
	byName            []*Font // fonts in load order
	charset, zoom     bool
	marks             *Bookmarks
	markedOnly        bool
	msg               string // one-shot status message for the info bar

	editing  int
	editOrig string

	dir   string
	noBG  bool // preview with black drawn as terminal background (x)
	listH int  // list rows shown by the last draw, for paging

	// Layout of the last draw, for mouse hit-testing.
	listY, prevY, prevH int
	buttons             tcell.ButtonMask // mouse buttons held at the last event

	width    int    // preview wrap width in columns, 0 = window width (w)
	widthStr string // width as edited
	noWidth  bool   // W: no wrap width at all; lines break only at hard limits

	irc      bool      // -irc: Enter outputs the preview for IRC
	maxBytes int       // longest IRC line allowed, in bytes
	picked   []string  // IRC lines chosen with Enter
	closeKey *closeKey // -close-key: cancel and exit
}

var (
	stDefault = tcell.StyleDefault
	stBar     = tcell.StyleDefault.Reverse(true)
	stOn      = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorYellow).Bold(true)
	stDim     = tcell.StyleDefault.Dim(true)
	stFull    = tcell.StyleDefault.Foreground(tcell.ColorGreen).Bold(true)
	stSel     = tcell.StyleDefault.Background(tcell.ColorBlue).Foreground(tcell.ColorWhite).Bold(true)
	stWarn    = tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorRed).Bold(true)
)

func (a *App) matches(f *Font) bool {
	if a.markedOnly && !a.marks.Has(f) {
		return false
	}
	if a.nameQ != "" && !strings.Contains(strings.ToLower(f.File+" "+f.Name), strings.ToLower(a.nameQ)) {
		return false
	}
	if t := typeFilters[a.typeFilter].typ; t >= 0 && f.Type != t {
		return false
	}
	for i, req := range a.require {
		if req == reqAll && f.Count[i] < classSize[i] || req == reqSome && f.Count[i] == 0 {
			return false
		}
	}
	for _, r := range a.glyphQ {
		if r != ' ' && !f.Has(r) {
			return false
		}
	}
	return true
}

// sortFonts reorders a.all by the current sort mode; ties keep load order.
func (a *App) sortFonts() {
	a.all = slices.Clone(a.byName)
	switch a.sortMode {
	case 1:
		slices.SortStableFunc(a.all, func(x, y *Font) int { return x.Height - y.Height })
	case 2:
		slices.SortStableFunc(a.all, func(x, y *Font) int { return y.Height - x.Height })
	}
	a.refilter()
	a.sel, a.top = 0, 0
}

func (a *App) refilter() {
	var cur *Font
	if a.sel < len(a.shown) {
		cur = a.shown[a.sel]
	}
	a.shown = a.shown[:0]
	a.sel = 0
	for _, f := range a.all {
		if a.matches(f) {
			if f == cur {
				a.sel = len(a.shown)
			}
			a.shown = append(a.shown, f)
		}
	}
	a.pscroll = 0
}

func (a *App) editTarget() *string {
	switch a.editing {
	case editName:
		return &a.nameQ
	case editGlyphs:
		return &a.glyphQ
	case editText:
		return &a.text
	case editWidth:
		return &a.widthStr
	}
	return nil
}

func (a *App) startEdit(mode int) {
	a.editing = mode
	a.editOrig = *a.editTarget()
}

func (a *App) puts(x, y int, s string, st tcell.Style) int {
	w, _ := a.s.Size()
	for _, r := range s {
		if x >= w {
			break
		}
		a.s.SetContent(x, y, r, nil, st)
		x++
	}
	return x
}

func (a *App) fillRow(y int, st tcell.Style) {
	w, _ := a.s.Size()
	for x := range w {
		a.s.SetContent(x, y, ' ', nil, st)
	}
}

// part is a run of text in one style; a bar item is one or more parts that
// must stay together on a line.
type part struct {
	s  string
	st tcell.Style
}

// foldItems wraps bar items onto as many lines as width w needs.
func foldItems(items [][]part, w int) [][][]part {
	var lines [][][]part
	var cur [][]part
	curW := 0
	for _, it := range items {
		iw := 0
		for _, p := range it {
			iw += len([]rune(p.s))
		}
		if len(cur) > 0 && curW+2+iw > w {
			lines = append(lines, cur)
			cur, curW = nil, 0
		}
		if len(cur) > 0 {
			curW += 2
		}
		cur = append(cur, it)
		curW += iw
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	return lines
}

// drawBar draws folded bar lines starting at row y over a filled background.
func (a *App) drawBar(y int, lines [][][]part, fill tcell.Style, filled bool) {
	for i, line := range lines {
		if filled {
			a.fillRow(y+i, fill)
		}
		x := 1
		for j, it := range line {
			if j > 0 {
				x += 2
			}
			for _, p := range it {
				x = a.puts(x, y+i, p.s, p.st)
			}
		}
	}
}

var helpItems = [][]part{
	{{"/ name", stBar}}, {{"g glyphs", stBar}}, {{"1-4 require all/some", stBar}},
	{{"f type", stBar}}, {{"s sort", stBar}}, {{"b bookmark", stBar}},
	{{"B marked-only", stBar}}, {{"t text", stBar}}, {{"w/W width/none", stBar}}, {{"tab charset", stBar}},
	{{"x nobg", stBar}}, {{"z zoom", stBar}}, {{"c clear", stBar}}, {{"q quit", stBar}},
}

var ircHelpItems = append([][]part{{{"enter send", stOn}}, {{"esc cancel", stBar}}}, helpItems...)

// wrapWidth is the column width previews are wrapped to.
func (a *App) wrapWidth() int {
	if a.width > 0 {
		return a.width
	}
	w, _ := a.s.Size()
	return max(1, w-1)
}

// render lays out text in f. The wrap width is a soft limit: words share a
// line only while it holds, but a lone wider word stays whole. The hard
// limit, which splits words evenly, is the IRC byte limit in IRC mode and
// nothing otherwise. The charset view is a reference grid, so there the
// width is hard too.
func (a *App) render(f *Font, text string, fallback, charset bool) ([][]cell, string) {
	maxw := a.wrapWidth()
	soft := func(gs []*glyph) bool { return f.Width(gs) <= maxw }
	hard := func(gs []*glyph) bool {
		return !a.irc || maxIRCBytes(f.Compose(gs)) <= a.maxBytes
	}
	if charset {
		hard = soft
	} else if a.noWidth {
		soft = func([]*glyph) bool { return true }
	}
	return f.Render(text, soft, hard, fallback)
}

// pick renders the preview text for IRC and returns false to quit on success.
func (a *App) pick() bool {
	if len(a.shown) == 0 {
		return true
	}
	if a.charset {
		a.msg = "leave charset view (tab) to send your text"
		return true
	}
	if strings.TrimSpace(a.text) == "" {
		a.msg = "no text to send (t)"
		return true
	}
	f := a.shown[a.sel]
	rows, _ := a.render(f, a.text, true, false)
	if n := maxIRCBytes(rows); n > a.maxBytes {
		a.msg = fmt.Sprintf("a single letter is too long for IRC (%d > %d bytes): pick a smaller font", n, a.maxBytes)
		return true
	}
	a.picked = IRCLines(rows)
	return false
}

func (a *App) draw() {
	s := a.s
	s.Clear()
	w, h := s.Size()

	// Header: active filters, folded to fit.
	header := [][]part{{{fmt.Sprintf("%d/%d fonts", len(a.shown), len(a.all)), stBar}}}
	if a.irc {
		header = append([][]part{{{"IRC", stWarn}}}, header...)
	}
	for i, n := range classNames {
		st := stDim
		if a.require[i] != reqOff {
			st = stOn
		}
		header = append(header, []part{{fmt.Sprintf("[%d]%s:%s", i+1, n, reqLabels[a.require[i]]), st}})
	}
	header = append(header,
		[]part{{"type:", stDefault}, {typeFilters[a.typeFilter].label, stOn}},
		[]part{{"sort:", stDefault}, {sortLabels[a.sortMode], stOn}})
	if a.noWidth {
		header = append(header, []part{{"width:", stDefault}, {"none", stOn}})
	} else if a.width > 0 {
		header = append(header, []part{{"width:", stDefault}, {strconv.Itoa(a.width), stOn}})
	}
	if a.irc {
		header = append(header, []part{{"max:", stDefault}, {fmt.Sprintf("%d bytes", a.maxBytes), stOn}})
	}
	if a.noBG {
		header = append(header, []part{{"nobg", stOn}})
	}
	if a.markedOnly {
		header = append(header, []part{{"★ bookmarks only", stOn}})
	}
	if a.nameQ != "" {
		header = append(header, []part{{"name:", stDefault}, {a.nameQ, stOn}})
	}
	if a.glyphQ != "" {
		header = append(header, []part{{"glyphs:", stDefault}, {a.glyphQ, stOn}})
	}
	headLines := foldItems(header, w-1)
	a.drawBar(0, headLines, stDefault, false)
	headH := len(headLines)

	// Footer: help folded to fit, or the single-line editor.
	help := helpItems
	if a.irc {
		help = ircHelpItems
	}
	footLines := foldItems(help, w-1)
	footH := len(footLines)
	if a.editing != editNone {
		footH = 1
	}

	listH := 0
	if !a.zoom {
		avail := h - headH - footH - 1
		listH = min(max(3, avail/3), len(a.shown))
	}
	a.listH, a.listY = listH, headH
	if a.sel < a.top {
		a.top = a.sel
	}
	if listH > 0 && a.sel >= a.top+listH {
		a.top = a.sel - listH + 1
	}
	a.top = max(0, min(a.top, len(a.shown)-listH))

	for i := range listH {
		f := a.shown[a.top+i]
		y := headH + i
		base := stDefault
		if a.top+i == a.sel {
			base = stSel
			a.fillRow(y, base)
		}
		mark := " "
		if a.marks.Has(f) {
			mark = "★"
		}
		x := a.puts(0, y, mark+fmt.Sprintf("%-*s %-12s %-7s %3d rows", a.fileW, f.File, f.Name, f.TypeName(), f.Height), base)
		for c, n := range classNames {
			st := base
			if a.top+i != a.sel {
				switch f.Count[c] {
				case classSize[c]:
					st = stFull
				case 0:
					st = stDim
				}
			}
			x = a.puts(x+2, y, fmt.Sprintf("%s %2d", n, f.Count[c]), st)
		}
	}

	// Info bar + preview.
	infoY := headH + listH
	a.fillRow(infoY, stBar)
	prevTop, prevH := infoY+1, h-infoY-1-footH
	a.prevY, a.prevH = prevTop, prevH
	if len(a.shown) == 0 {
		a.puts(1, infoY, "no fonts match", stBar)
	} else {
		f := a.shown[a.sel]
		text, fallback := a.text, true
		if a.charset || strings.TrimSpace(text) == "" {
			text, fallback = f.Charset(), false
		}
		rows, missing := a.render(f, text, fallback, !fallback)
		a.pscroll = max(0, min(a.pscroll, len(rows)-prevH))
		info := fmt.Sprintf(" %s / %s · %s · spacing %d", f.File, f.Name, f.TypeName(), f.Spacing)
		if a.marks.Has(f) {
			info = " ★" + info
		}
		if a.charset {
			info += " · charset"
		}
		if missing != "" {
			info += " · missing: " + missing
		}
		if a.irc && !a.charset {
			n := maxIRCBytes(rows)
			info += fmt.Sprintf(" · %d lines, longest %d bytes", len(rows), n)
			if n > a.maxBytes && a.msg == "" {
				a.msg = "too long for IRC"
			}
		}
		widest := 0
		for _, r := range rows {
			widest = max(widest, len(r))
		}
		if widest > w {
			info += fmt.Sprintf(" · %d cols, cut off at window edge", widest)
		} else if a.width > 0 && !a.noWidth && widest > a.width {
			info += fmt.Sprintf(" · %d cols, wider than width %d", widest, a.width)
		}
		if len(rows) > prevH {
			info += fmt.Sprintf(" · rows %d-%d/%d", a.pscroll+1, min(a.pscroll+prevH, len(rows)), len(rows))
		}
		a.puts(0, infoY, info, stBar)
		for i := 0; i < prevH && a.pscroll+i < len(rows); i++ {
			for x, c := range rows[a.pscroll+i] {
				if x >= w {
					break
				}
				r, st := c.r, cellStyle(c.a)
				if a.noBG {
					r, st = noBGLook(c)
				}
				s.SetContent(x, prevTop+i, r, nil, st)
			}
		}
	}
	if a.msg != "" {
		a.puts(max(0, w-len([]rune(a.msg))-1), infoY, a.msg, stWarn)
		a.msg = ""
	}

	// Footer.
	fy := h - footH
	s.HideCursor()
	if a.editing != editNone {
		a.fillRow(fy, stBar)
		prompt := map[int]string{
			editName:   " filter by file/font name: ",
			editGlyphs: " must contain these glyphs: ",
			editText:   " preview text: ",
			editWidth:  " wrap width in columns (empty = window width): ",
		}[a.editing]
		x := a.puts(0, fy, prompt, stBar)
		x = a.puts(x, fy, *a.editTarget(), stBar)
		s.ShowCursor(x, fy)
		a.puts(max(x+2, w-28), fy, "enter=ok esc=cancel", stBar)
	} else {
		a.drawBar(fy, footLines, stBar, true)
	}
	s.Show()
}

func (a *App) handleEdit(ev *tcell.EventKey) {
	p := a.editTarget()
	switch ev.Key() {
	case tcell.KeyEnter:
		a.editing = editNone
		return
	case tcell.KeyEscape:
		*p = a.editOrig
		a.editing = editNone
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if r := []rune(*p); len(r) > 0 {
			*p = string(r[:len(r)-1])
		}
	case tcell.KeyCtrlU:
		*p = ""
	case tcell.KeyRune:
		*p += string(ev.Rune())
	default:
		return
	}
	switch p {
	case &a.text:
	case &a.widthStr:
		a.width, _ = strconv.Atoi(strings.TrimSpace(a.widthStr))
		a.width = max(0, a.width)
	default:
		a.refilter()
	}
}

// handleMouse scrolls the list or preview under the pointer with the wheel,
// and selects a font when its list row is clicked.
func (a *App) handleMouse(ev *tcell.EventMouse) {
	_, y := ev.Position()
	btn := ev.Buttons()
	pressed := btn &^ a.buttons // newly pressed, not held from before
	a.buttons = btn
	inList := y >= a.listY && y < a.listY+a.listH
	inPreview := y >= a.prevY && y < a.prevY+a.prevH
	const wheelStep = 3
	switch {
	case btn&tcell.WheelUp != 0 && inList:
		a.sel = max(0, a.sel-wheelStep)
		a.pscroll = 0
	case btn&tcell.WheelDown != 0 && inList:
		a.sel = max(0, min(a.sel+wheelStep, len(a.shown)-1))
		a.pscroll = 0
	case btn&tcell.WheelUp != 0 && inPreview:
		a.pscroll -= wheelStep
	case btn&tcell.WheelDown != 0 && inPreview:
		a.pscroll += wheelStep
	case pressed&tcell.Button1 != 0 && inList && a.editing == editNone:
		if i := a.top + y - a.listY; i < len(a.shown) && i != a.sel {
			a.sel = i
			a.pscroll = 0
		}
	}
}

// handleKey returns false when the app should quit.
func (a *App) handleKey(ev *tcell.EventKey) bool {
	if a.closeKey.matches(ev) {
		return false
	}
	if a.editing != editNone {
		a.handleEdit(ev)
		return true
	}
	page := max(1, a.listH)
	move := func(d int) {
		a.sel = max(0, min(a.sel+d, len(a.shown)-1))
		a.pscroll = 0
	}
	switch ev.Key() {
	case tcell.KeyCtrlC:
		return false
	case tcell.KeyEnter:
		if a.irc {
			return a.pick()
		}
	case tcell.KeyEscape:
		if a.irc {
			return false
		}
	case tcell.KeyUp:
		move(-1)
	case tcell.KeyDown:
		move(1)
	case tcell.KeyPgUp:
		move(-page)
	case tcell.KeyPgDn:
		move(page)
	case tcell.KeyHome:
		move(-len(a.shown))
	case tcell.KeyEnd:
		move(len(a.shown))
	case tcell.KeyTab:
		a.charset = !a.charset
		a.pscroll = 0
	case tcell.KeyRune:
		switch r := ev.Rune(); r {
		case 'q':
			return false
		case 'k':
			move(-1)
		case 'j':
			move(1)
		case 'K':
			a.pscroll -= 3
		case 'J':
			a.pscroll += 3
		case '/':
			a.startEdit(editName)
		case 'g':
			a.startEdit(editGlyphs)
		case 't':
			a.startEdit(editText)
		case 'w':
			a.noWidth = false
			a.startEdit(editWidth)
		case 'W':
			a.noWidth = !a.noWidth
			a.pscroll = 0
		case '1', '2', '3', '4':
			a.require[r-'1'] = (a.require[r-'1'] + 1) % len(reqLabels)
			a.refilter()
		case 's':
			a.sortMode = (a.sortMode + 1) % len(sortLabels)
			a.sortFonts()
		case 'f':
			a.typeFilter = (a.typeFilter + 1) % len(typeFilters)
			a.refilter()
		case 'b':
			if a.sel < len(a.shown) {
				if err := a.marks.Toggle(a.shown[a.sel]); err != nil {
					a.msg = "bookmark save failed: " + err.Error()
				}
				if a.markedOnly {
					a.refilter()
				}
			}
		case 'B':
			a.markedOnly = !a.markedOnly
			a.refilter()
		case 'x':
			a.noBG = !a.noBG
		case 'z':
			a.zoom = !a.zoom
		case 'c':
			a.require = [4]int{}
			a.typeFilter, a.nameQ, a.glyphQ = 0, "", ""
			a.markedOnly = false
			a.refilter()
		}
	}
	return true
}

// version is set at build time: go build -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	irc := flag.Bool("irc", false, "IRC mode: Enter prints the preview as mIRC-colored lines on stdout and exits\n(Esc/q exit 1 without output)")
	width := flag.Int("width", 0, "wrap width in columns (0 = window width)")
	maxBytes := flag.Int("max-bytes", 400, "IRC mode: longest line allowed, in bytes")
	text := flag.String("text", "yes hello", "initial preview text")
	glyphs := flag.String("glyphs", "", "initial glyph filter: only fonts with all these characters")
	closeKeyName := flag.String("close-key", "", "extra key that cancels and exits, e.g. meta-f or ctrl-f\n(lets the key that opened a popup also close it)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: tdfbrowse [flags] [font-dir]\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("tdfbrowse", version)
		return
	}
	ck, err := parseCloseKey(*closeKeyName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	os.Exit(run(dir, options{irc: *irc, width: max(0, *width), maxBytes: *maxBytes,
		text: *text, glyphs: *glyphs, closeKey: ck}))
}

type options struct {
	irc             bool
	width, maxBytes int
	text, glyphs    string
	closeKey        *closeKey
}

func run(dir string, o options) int {
	fonts, err := LoadDir(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(fonts) == 0 {
		fmt.Fprintf(os.Stderr, "no .TDF fonts found in %s\nusage: tdfbrowse [flags] [font-dir]\n", dir)
		return 1
	}

	marks, err := LoadBookmarks(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reading bookmarks:", err)
		return 1
	}

	// tcell draws on /dev/tty, leaving stdout free for -irc output.
	s, err := tcell.NewScreen()
	if err == nil {
		err = s.Init()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	s.EnableMouse()

	a := &App{s: s, all: fonts, byName: fonts, text: o.text, glyphQ: o.glyphs, marks: marks, dir: dir,
		irc: o.irc, noBG: o.irc, maxBytes: o.maxBytes, width: o.width, closeKey: o.closeKey}
	if o.width > 0 {
		a.widthStr = strconv.Itoa(o.width)
	}
	for _, f := range fonts {
		a.fileW = max(a.fileW, len(f.File))
	}
	a.refilter()
	for running := true; running; {
		a.draw()
		switch ev := s.PollEvent().(type) {
		case *tcell.EventResize:
			s.Sync()
		case *tcell.EventKey:
			running = a.handleKey(ev)
		case *tcell.EventMouse:
			a.handleMouse(ev)
		}
	}
	s.Fini()

	if !o.irc {
		return 0
	}
	if a.picked == nil {
		return 1
	}
	for _, l := range a.picked {
		fmt.Println(l)
	}
	return 0
}
