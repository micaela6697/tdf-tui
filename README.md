# tdfbrowse

A terminal browser for TheDraw (`.TDF`) ANSI fonts. Preview any text in any
font, filter the collection by which characters each font actually has, and
bookmark the ones you like. An IRC mode plus a WeeChat plugin let you pick a
font in a tmux popup and send the banner straight to a channel.

## Build

```sh
make            # or: go build -o tdfbrowse .
make test       # go vet + tests
make install    # tdfbrowse to ~/.local/bin, the WeeChat script to WeeChat's home
make uninstall
```

`PREFIX` and `WEECHAT_DIR` override where `make install` puts things.
WeeChat's home defaults to `~/.weechat` if it exists, else
`~/.local/share/weechat`, the same rule WeeChat follows. `tdfbrowse -version`
prints the version (`git describe`) it was built from.

Needs Go 1.27+. The only dependency is [tcell](https://github.com/gdamore/tcell).

## Usage

```sh
tdfbrowse [flags] [font-dir]     # font-dir defaults to the current directory
```

Every `.TDF` file directly inside `font-dir` is loaded; a single file can hold
several fonts, and each is listed separately. Each row shows the file, font
name, type (color/block/outline), height in rows, and how many of A-Z, a-z,
0-9 and punctuation it defines: green means the set is complete, dim means
none.

### Keys

| Key | Action |
|---|---|
| `↑` `↓` `j` `k` PgUp PgDn Home End | Move through the font list |
| `J` / `K` | Scroll the preview |
| Mouse | Click a font to select it; the wheel scrolls the list or the preview |
| `/` | Filter by file or font name (live) |
| `g` | Only fonts that have all of the typed characters, e.g. `@#&` (live) |
| `1` `2` `3` `4` | Cycle A-Z / a-z / 0-9 / punctuation requirement: off → all → some |
| `f` | Cycle font type: all / color / block / outline |
| `s` | Sort: name / height ↑ / height ↓ |
| `b` | Bookmark the selected font (★) |
| `B` | Show bookmarked fonts only |
| `t` | Edit the preview text |
| `w` | Set the wrap width in columns (empty = window width) |
| `W` | Toggle no width limit |
| `Tab` | Show every glyph the font defines |
| `x` | nobg: draw black as the terminal's background |
| `z` | Zoom: hide the list, full-height preview |
| `c` | Clear all filters |
| `q` / Ctrl-C | Quit |

In text fields: Enter accepts, Esc cancels, Ctrl-U clears.

The header and help bars fold onto extra lines in narrow windows.

### Flags

| Flag | Meaning |
|---|---|
| `-text STR` | Initial preview text (default `yes hello`) |
| `-width N` | Wrap width in columns (0 = window width) |
| `-glyphs STR` | Start filtered to fonts that have all these characters |
| `-irc` | IRC mode, see below |
| `-max-bytes N` | IRC mode: longest line allowed, in bytes (default 400) |
| `-close-key KEY` | Extra key that cancels and exits, e.g. `meta-f` or `ctrl-f` |

### Wrapping

The wrap width is a preference: words share a line only while it fits, but a
single word wider than the width stays whole (the info bar notes it). A word
wider than the width breaks at punctuation first: before `. @ # :` and after
`- / _ , ; ! ? &`, so `0xfdb.xyz` becomes `0xfdb` / `.xyz`. Only in IRC mode,
and only when a piece is still over the byte limit, is it split into pieces of
even width.

A missing letter falls back to its other case, since many fonts define only
capitals; characters the font can't draw at all are listed in the info bar.

### Bookmarks

Saved to `tdfbrowse-bookmarks.txt` inside the font directory, so each
collection keeps its own. It is plain text, one font per line:
`file<TAB>font index in file<TAB>font name`.

### nobg

TDF color fonts store empty cells as an explicit black background. Many
terminal themes draw palette "black" as a gray, which shows up as boxes
behind the letters. `x` draws black as the terminal's own background instead
(swapping half-block characters where black is the foreground, so shapes are
kept). Other colors, including gray shading, are untouched.

## IRC mode

With `-irc`, Enter prints the preview as mIRC-colored lines on stdout and
exits with status 0; Esc or `q` exits with status 1 and prints nothing. The
screen is drawn on `/dev/tty`, so stdout can be redirected:

```sh
tdfbrowse -irc -width 80 -text 'hello' ~/fonts > banner.txt
```

In this mode:

- nobg is on, and the output uses the same rule: black is sent as no
  background.
- Lines are wrapped to fit `-max-bytes` as well as the width. Block characters
  are 3 bytes each in UTF-8, plus the color codes.
- The info bar shows the line count and the longest line in bytes.
- Blank rows are printed as empty lines.

## WeeChat plugin

`weechat/tdf.py` adds a `/tdf` command that opens tdfbrowse in a tmux popup
and sends the chosen banner to the current channel or query. It needs WeeChat
running inside tmux 3.2 or newer.

### Setup

```sh
make install
```

```
/script load tdf.py
/set plugins.var.python.tdf.font_dir ~/path/to/THEDRAWFONTS
/set plugins.var.python.tdf.tdfbrowse ~/.local/bin/tdfbrowse
/key bind meta-f /tdf
```

After updating, `make install` and `/script reload tdf.py`; a new binary alone
needs no reload, since each `/tdf` starts it fresh.

### Use

- Type some text in the input bar, press the key, pick a font, press Enter.
  The input bar is cleared once the banner is sent.
- Press the same key again (or Esc/`q`) to close the popup without sending.
- `/tdf some text` or `/tdf -w 50 some text` uses the given text instead.
- The list starts filtered to fonts that have the text's digits and
  punctuation (`g` changes or clears it).

The plugin works out the byte limit for each line: an IRC message is at most
512 bytes including the `:nick!user@host` prefix the server adds. By default
it assumes the longest possible prefix (63-byte host, 12-byte user name), as
bouncers like soju do when they split long lines; a line between that and
your real limit would otherwise be cut in two by the bouncer, the second half
losing its colors. WeeChat also splits long messages by its own reckoning
(`irc.server.*.split_msg_max_length` and its estimate of the prefix), so the
limit is lowered to what WeeChat sends whole, and each line is checked again
before sending: a banner that would be split isn't sent. Lines are sent through WeeChat's normal IRC input path
(`irc_input_send`), so leading spaces are kept and WeeChat's anti-flood
setting paces them.

### Options

`/set plugins.var.python.tdf.*`

| Option | Default | Meaning |
|---|---|---|
| `tdfbrowse` | `tdfbrowse` | Binary name in `$PATH` or a full path |
| `font_dir` | | Directory with the `.TDF` fonts (required) |
| `width` | `80` | Wrap width in columns |
| `popup_width` | `95%` | tmux popup width |
| `popup_height` | `90%` | tmux popup height |
| `close_key` | | Key that closes the popup; empty: the key bound to `/tdf`; `none`: disabled |
| `real_host` | `off` | `on`: size lines for your actual host instead of the longest possible one (a little more room, only safe without a bouncer) |
| `art_colors` | `on` | Show ANSI art lines with exact terminal colors (see below) |
| `prefix_reserve` | `100` | With `real_host` on: bytes assumed for your `:nick!user@host` prefix when it can't be looked up (e.g. in queries) |

### Exact colors for ANSI art

With `weechat.look.color_basic_force_bold` on, WeeChat draws light colors as
the normal color in bold, so light cyan shading over a cyan background turns
into solid cyan. With `weechat.look.color_real_white` off, white is drawn in
the terminal's default color.

To keep those settings for normal chat, the plugin rewrites the colors of
lines that look like ANSI art (three or more block or box-drawing characters)
from WeeChat's basic colors to the same colors by palette number, which those
settings don't touch. This applies to art from anyone, not just your own.
Turn it off with `/set plugins.var.python.tdf.art_colors off`.
