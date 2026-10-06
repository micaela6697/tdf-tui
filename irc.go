package main

import (
	"fmt"
	"strings"
)

// DOS color index -> mIRC color number.
var dosToMIRC = [16]int{1, 2, 3, 10, 5, 6, 7, 15, 14, 12, 9, 11, 4, 13, 8, 0}

// Block characters paired with their visual inverse: drawing inv[r] with fg
// and bg swapped looks the same as drawing r.
var inverse = map[rune]rune{'▀': '▄', '▄': '▀', '▌': '▐', '▐': '▌', '░': '▓', '▓': '░', '▒': '▒', '█': ' ', ' ': '█'}

// noBGCell resolves a cell with black treated as "no color": it returns the
// rune to draw and its DOS fg/bg, with -1 meaning the viewer's default.
// Black foreground on block characters is swapped for the inverse character
// so the shape survives.
func noBGCell(c cell) (r rune, fg, bg int) {
	if c.a < 0 {
		return c.r, -1, -1
	}
	r, fg, bg = c.r, c.a&15, c.a>>4&15
	if fg == 0 {
		if inv, ok := inverse[r]; ok {
			r, fg, bg = inv, bg, 0
		}
	}
	if bg == 0 {
		bg = -1
	}
	if r == ' ' || fg == 0 && bg == -1 {
		return ' ', -1, bg
	}
	return r, fg, bg
}

// ircLine encodes one row of cells as mIRC-colored text, with black as
// no background and trailing blanks dropped.
func ircLine(row []cell) string {
	type look struct {
		r      rune
		fg, bg int
	}
	looks := make([]look, len(row))
	end := 0
	for i, c := range row {
		r, fg, bg := noBGCell(c)
		looks[i] = look{r, fg, bg}
		if r != ' ' || bg >= 0 {
			end = i + 1
		}
	}
	var sb strings.Builder
	curFG, curBG := -1, -1
	for _, l := range looks[:end] {
		blank := l.r == ' '
		switch {
		case l.bg < 0 && curBG >= 0:
			// No way to clear only the background: reset everything.
			sb.WriteByte(0x0f)
			curFG, curBG = -1, -1
			if !blank && l.fg >= 0 {
				fmt.Fprintf(&sb, "\x03%02d", dosToMIRC[l.fg])
				curFG = l.fg
				if l.r == ',' {
					sb.WriteString("\x02\x02")
				}
			}
		case l.bg >= 0 && (l.bg != curBG || !blank && l.fg != curFG):
			fg := l.fg
			if fg < 0 || blank {
				fg = max(curFG, 0) // a blank shows no foreground; keep the cheapest
			}
			fmt.Fprintf(&sb, "\x03%02d,%02d", dosToMIRC[fg], dosToMIRC[l.bg])
			curFG, curBG = fg, l.bg
		case !blank && l.fg >= 0 && l.fg != curFG:
			fmt.Fprintf(&sb, "\x03%02d", dosToMIRC[l.fg])
			if curBG >= 0 {
				fmt.Fprintf(&sb, ",%02d", dosToMIRC[curBG])
			}
			curFG = l.fg
			if curBG < 0 && l.r == ',' {
				sb.WriteString("\x02\x02") // keep "," from reading as a background
			}
		}
		sb.WriteRune(l.r)
	}
	return sb.String()
}

// IRCLines encodes rendered rows for IRC, one message per row.
func IRCLines(rows [][]cell) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = ircLine(r)
	}
	return out
}

// maxIRCBytes is the byte length of the longest encoded row.
func maxIRCBytes(rows [][]cell) int {
	n := 0
	for _, r := range rows {
		n = max(n, len(ircLine(r)))
	}
	return n
}
