package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// closeKey is an extra key that cancels and exits, so the key that opened
// tdfbrowse (e.g. a WeeChat binding) can also close it.
type closeKey struct {
	alt, ctrl bool
	r         rune
}

// parseCloseKey accepts WeeChat/tmux style names: meta-f, alt-f, M-f,
// ctrl-f, C-f.
func parseCloseKey(s string) (*closeKey, error) {
	if s == "" {
		return nil, nil
	}
	low := strings.ToLower(s)
	var k closeKey
	for _, p := range []struct {
		prefix    string
		alt, ctrl bool
	}{{"meta-", true, false}, {"alt-", true, false}, {"m-", true, false}, {"ctrl-", false, true}, {"c-", false, true}} {
		if strings.HasPrefix(low, p.prefix) {
			k.alt, k.ctrl = p.alt, p.ctrl
			s = s[len(p.prefix):]
			break
		}
	}
	r, n := utf8.DecodeRuneInString(s)
	if !k.alt && !k.ctrl || n == 0 || n != len(s) {
		return nil, fmt.Errorf("unsupported close key %q (use e.g. meta-f or ctrl-f)", s)
	}
	if k.ctrl {
		r = []rune(strings.ToLower(string(r)))[0]
		if r < 'a' || r > 'z' {
			return nil, fmt.Errorf("unsupported close key: ctrl only works with letters")
		}
	}
	k.r = r
	return &k, nil
}

func (k *closeKey) matches(ev *tcell.EventKey) bool {
	if k == nil {
		return false
	}
	if k.alt {
		return ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModAlt != 0 && ev.Rune() == k.r
	}
	return ev.Key() == tcell.KeyCtrlA+tcell.Key(k.r-'a')
}
