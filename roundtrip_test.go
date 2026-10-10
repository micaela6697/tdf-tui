package main

import (
	"strings"
	"testing"
)

var mircToDOS = func() map[int]int {
	m := map[int]int{}
	for d, c := range dosToMIRC {
		m[c] = d
	}
	return m
}()

// decodeIRC reads mIRC-coded text the way IRC clients do, returning per
// column the rune and DOS fg/bg (-1 = default).
func decodeIRC(s string) (rs []rune, fgs, bgs []int) {
	fg, bg := -1, -1
	r := []rune(s)
	num := func(i int) (int, int) { // up to 2 digits
		n, j := 0, i
		for j < len(r) && j < i+2 && r[j] >= '0' && r[j] <= '9' {
			n = n*10 + int(r[j]-'0')
			j++
		}
		if j == i {
			return -1, i
		}
		return n, j
	}
	for i := 0; i < len(r); {
		switch r[i] {
		case 0x03:
			f, j := num(i + 1)
			if f < 0 {
				fg, bg = -1, -1
				i = j
				continue
			}
			fg = mircToDOS[f]
			if j+1 < len(r) && r[j] == ',' {
				if b, k := num(j + 1); b >= 0 {
					bg = mircToDOS[b]
					j = k
				}
			}
			i = j
		case 0x0f:
			fg, bg = -1, -1
			i++
		case 0x02:
			i++
		default:
			rs = append(rs, r[i])
			fgs = append(fgs, fg)
			bgs = append(bgs, bg)
			i++
		}
	}
	return
}

func TestIRCRoundTrip(t *testing.T) {
	fonts, err := LoadDir("..")
	if err != nil || len(fonts) == 0 {
		t.Skip("no fonts")
	}
	bad := 0
	for _, f := range fonts {
		all := func([]*glyph) bool { return true }
		rows, _ := f.Render(strings.ReplaceAll(f.Charset(), " ", ""), all, all, false)
		for y, row := range rows {
			line := ircLine(row)
			rs, fgs, bgs := decodeIRC(line)
			for x, c := range row {
				wr, wfg, wbg := noBGCell(c)
				var gr rune = ' '
				gfg, gbg := -1, -1
				if x < len(rs) {
					gr, gfg, gbg = rs[x], fgs[x], bgs[x]
				}
				ok := gr == wr && gbg == wbg && (wr == ' ' || gfg == wfg)
				if !ok {
					bad++
					if bad <= 15 {
						t.Errorf("%s/%s row %d col %d: want %q fg%d bg%d, got %q fg%d bg%d\n  %q",
							f.File, f.Name, y, x, wr, wfg, wbg, gr, gfg, gbg, line)
					}
				}
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d mismatched cells", bad)
	}
}
