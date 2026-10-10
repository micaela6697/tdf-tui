# tdf.py - send TheDraw font banners to IRC.
#
# /tdf opens tdfbrowse in a tmux popup. Pick a font, type your text (t),
# press Enter, and the banner is sent to the channel or query you ran /tdf
# in. Esc or q cancels without sending anything.
#
# With no text argument, /tdf uses what's typed in the input bar, so a key
# binding works well: type a word, press the key, pick a font, Enter.
#   /key bind meta-t /tdf
# Pressing the same key again inside the popup closes it.
#
# Requires WeeChat running inside tmux (3.2+) and tdfbrowse built with -irc
# support. Set the font directory once:
#   /set plugins.var.python.tdf.font_dir ~/path/to/THEDRAWFONTS

import os
import re
import shlex
import tempfile

import weechat

SCRIPT_NAME = "tdf"
SCRIPT_AUTHOR = "alban"
SCRIPT_VERSION = "1.0"
SCRIPT_LICENSE = "MIT"
SCRIPT_DESC = "Send TheDraw font banners picked in a tdfbrowse tmux popup"

OPTIONS = {
    "tdfbrowse": ("tdfbrowse", "tdfbrowse binary: a name in $PATH or a full path"),
    "font_dir": ("", "directory containing the .TDF fonts (required)"),
    "width": ("80", "wrap width in columns for the banner"),
    "popup_width": ("95%", "tmux popup width (columns or percentage)"),
    "popup_height": ("90%", "tmux popup height (lines or percentage)"),
    "close_key": (
        "",
        "key that closes the popup, e.g. meta-f; empty: the key bound to /tdf "
        "(so it toggles the popup); none: disabled",
    ),
    "art_colors": (
        "on",
        "show ANSI art lines with exact terminal colors: light colors stay "
        "real bright colors instead of bold (weechat.look.color_basic_force_bold) "
        "and white is real white, without changing other messages",
    ),
    "prefix_reserve": (
        "100",
        "bytes reserved for the :nick!user@host prefix the server adds, "
        "used when your host can't be looked up (e.g. in queries)",
    ),
}

# IRC messages are at most 512 bytes including the trailing CRLF.
IRC_LINE_MAX = 510

# A blank row can't be sent as an empty message; bold on/off is invisible.
BLANK_LINE = "\x02\x02"

# Popups waiting to finish: job id -> details.
jobs = {}
job_seq = 0


def opt(name):
    return weechat.config_get_plugin(name)


def error(buffer, msg):
    weechat.prnt(buffer, "%s%s: %s" % (weechat.prefix("error"), SCRIPT_NAME, msg))


# WeeChat basic color number -> terminal palette color, for art lines.
# Order: default, black, darkgray, red, lightred, green, lightgreen, brown,
# yellow, blue, lightblue, magenta, lightmagenta, cyan, lightcyan, gray, white.
BASIC_TO_TERM = [None, 0, 8, 1, 9, 2, 10, 3, 11, 4, 12, 5, 13, 6, 14, 7, 15]

# WeeChat color codes in a displayed line: fg (\x19F), bg (\x19B), or
# fg+bg (\x19*), each color either basic ("14") or extended ("@00014").
ATTRS = r"[*!/_|%.]*"
COLOR = r"(?:@\d{5}|\d\d)"
COLOR_CODE = re.compile(
    r"\x19(?:F(" + ATTRS + r")(" + COLOR + r")"
    r"|B(" + COLOR + r")"
    r"|\*(" + ATTRS + r")(" + COLOR + r")(?:([,~])(" + COLOR + r"))?)"
)
ART_CHAR = re.compile("[\u2500-\u259f]")  # box drawing and block elements


def extended(color, attrs=""):
    """Color with its attributes, basic colors made extended: ("14", "|") ->
    "@|00014" (WeeChat puts attributes after the "@"). Extended and default
    colors are returned unchanged."""
    if color.startswith("@"):
        return attrs + color
    n = int(color)
    if n >= len(BASIC_TO_TERM) or BASIC_TO_TERM[n] is None:
        return attrs + color
    return "@%s%05d" % (attrs, BASIC_TO_TERM[n])


def to_extended_colors(message):
    def sub(m):
        if m.group(2) is not None:
            return "\x19F" + extended(m.group(2), m.group(1))
        if m.group(3) is not None:
            return "\x19B" + extended(m.group(3))
        code = "\x19*" + extended(m.group(5), m.group(4))
        if m.group(7) is not None:
            code += m.group(6) + extended(m.group(7))
        return code
    return COLOR_CODE.sub(sub, message)


def art_colors_cb(data, modifier, modifier_data, string):
    """Give ANSI art lines exact colors, unaffected by force_bold."""
    if opt("art_colors") != "on" or "\x19" not in string:
        return string
    prefix, sep, message = string.partition("\t")
    if not sep:
        prefix, message = "", string
    if len(ART_CHAR.findall(message)) < 3:
        return string
    return prefix + sep + to_extended_colors(message)


def own_prefix_len(server, channel):
    """Byte length of ':nick!user@host ' as the server will relay it."""
    nick = weechat.info_get("irc_nick", server)
    host = ""
    infolist = weechat.infolist_get("irc_nick", "", "%s,%s,%s" % (server, channel, nick))
    if infolist:
        if weechat.infolist_next(infolist):
            host = weechat.infolist_string(infolist, "host")
        weechat.infolist_free(infolist)
    if not nick or not host:
        try:
            return int(opt("prefix_reserve"))
        except ValueError:
            return 100
    return len((":%s!%s " % (nick, host)).encode("utf-8"))


def close_key():
    """Key tdfbrowse should treat as cancel: the option, else the /tdf binding."""
    key = opt("close_key").strip()
    if key.lower() == "none":
        return ""
    if not key:
        infolist = weechat.infolist_get("key", "", "default")
        if infolist:
            while weechat.infolist_next(infolist):
                if weechat.infolist_string(infolist, "command").split()[:1] == ["/tdf"]:
                    key = weechat.infolist_string(infolist, "key")
                    break
            weechat.infolist_free(infolist)
    # tdfbrowse understands meta-<char> and ctrl-<letter>; skip anything else.
    if re.fullmatch(r"(?i)(meta-.|ctrl-[a-z])", key):
        return key
    return ""


def tdf_cmd_cb(data, buffer, args):
    global job_seq

    if weechat.buffer_get_string(buffer, "localvar_type") not in ("channel", "private"):
        error(buffer, "run /tdf in a channel or query buffer")
        return weechat.WEECHAT_RC_OK
    if not os.environ.get("TMUX"):
        error(buffer, "WeeChat is not running inside tmux")
        return weechat.WEECHAT_RC_OK
    font_dir = os.path.expanduser(opt("font_dir"))
    if not font_dir or not os.path.isdir(font_dir):
        error(buffer, "set the font directory: /set plugins.var.python.tdf.font_dir <dir>")
        return weechat.WEECHAT_RC_OK

    width = opt("width")
    words = args.split(None, 2)
    if len(words) >= 2 and words[0] == "-w":
        width = words[1]
        text = words[2] if len(words) > 2 else ""
    else:
        text = args.strip()
    from_input = False
    if not text:
        # Nothing given: use what's typed in the input bar (cleared once sent).
        text = weechat.buffer_get_string(buffer, "input").strip()
        from_input = bool(text)
    if not width.isdigit():
        error(buffer, "width must be a number: %s" % width)
        return weechat.WEECHAT_RC_OK

    server = weechat.buffer_get_string(buffer, "localvar_server")
    target = weechat.buffer_get_string(buffer, "localvar_channel")
    max_bytes = (
        IRC_LINE_MAX
        - len(("PRIVMSG %s :" % target).encode("utf-8"))
        - own_prefix_len(server, target)
    )
    if max_bytes < 50:
        error(buffer, "not enough room in an IRC line (%d bytes)" % max_bytes)
        return weechat.WEECHAT_RC_OK

    fd, outfile = tempfile.mkstemp(prefix="tdf-", suffix=".txt")
    os.close(fd)

    argv = [
        os.path.expanduser(opt("tdfbrowse")),
        "-irc",
        "-width", width,
        "-max-bytes", str(max_bytes),
    ]
    if text:
        argv += ["-text", text]
        # Start filtered to fonts that have the text's digits and punctuation;
        # letters are left out since a missing one falls back to its other case.
        glyphs = "".join(sorted({c for c in text if "!" <= c <= "~" and not c.isalpha()}))
        if glyphs:
            argv += ["-glyphs", glyphs]
    key = close_key()
    if key:
        argv += ["-close-key", key]
    argv.append(font_dir)
    inner = "%s > %s" % (" ".join(shlex.quote(a) for a in argv), shlex.quote(outfile))

    # Only options available since tmux 3.2 (popup titles, -T, need 3.3).
    tmux_args = ["display-popup", "-E",
                 "-w", opt("popup_width"), "-h", opt("popup_height")]
    if os.environ.get("TMUX_PANE"):
        tmux_args += ["-t", os.environ["TMUX_PANE"]]
    tmux_args.append(inner)

    job_seq += 1
    job = str(job_seq)
    jobs[job] = {"server": server, "target": target, "outfile": outfile,
                 "input": text if from_input else None}
    options = {"arg%d" % (i + 1): a for i, a in enumerate(tmux_args)}
    weechat.hook_process_hashtable("tmux", options, 0, "tdf_done_cb", job)
    return weechat.WEECHAT_RC_OK


def tdf_done_cb(data, command, rc, out, err):
    if rc == weechat.WEECHAT_HOOK_PROCESS_RUNNING:
        return weechat.WEECHAT_RC_OK
    job = jobs.pop(data, None)
    if job is None:
        return weechat.WEECHAT_RC_OK

    try:
        with open(job["outfile"], encoding="utf-8", errors="replace") as fh:
            banner = fh.read()
    except OSError as e:
        banner = ""
        error("", "can't read banner: %s" % e)
    finally:
        try:
            os.remove(job["outfile"])
        except OSError:
            pass

    buffer = weechat.buffer_search("irc", "%s.%s" % (job["server"], job["target"]))
    if rc == weechat.WEECHAT_HOOK_PROCESS_ERROR or (rc != 0 and err.strip()):
        error(buffer, "tmux popup failed: %s" % (err.strip() or "could not run tmux"))
        return weechat.WEECHAT_RC_OK

    lines = banner.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    if not lines:
        return weechat.WEECHAT_RC_OK  # cancelled in tdfbrowse
    if not buffer:
        error("", "%s is no longer open; banner not sent" % job["target"])
        return weechat.WEECHAT_RC_OK

    for line in lines:
        if not line.strip():
            line = BLANK_LINE
        weechat.hook_signal_send(
            "irc_input_send",
            weechat.WEECHAT_HOOK_SIGNAL_STRING,
            "%s;%s;priority_high,user_message;;%s" % (job["server"], job["target"], line),
        )
    # The banner replaced what was typed; clear it unless it was edited meanwhile.
    if job["input"] is not None and weechat.buffer_get_string(buffer, "input").strip() == job["input"]:
        weechat.buffer_set(buffer, "input", "")
    return weechat.WEECHAT_RC_OK


if weechat.register(SCRIPT_NAME, SCRIPT_AUTHOR, SCRIPT_VERSION, SCRIPT_LICENSE,
                    SCRIPT_DESC, "", ""):
    for name, (default, desc) in OPTIONS.items():
        if not weechat.config_is_set_plugin(name):
            weechat.config_set_plugin(name, default)
        weechat.config_set_desc_plugin(name, desc)
    weechat.hook_modifier("weechat_print", "art_colors_cb", "")
    weechat.hook_command(
        "tdf",
        SCRIPT_DESC,
        "[-w <width>] [<text>]",
        "   -w: wrap width in columns for this banner (default: option width)\n"
        " text: banner text (default: the input bar contents, which are cleared\n"
        "       once the banner is sent); change it in the popup with t.\n"
        "       The font list starts filtered to fonts that have the text's\n"
        "       digits and punctuation (change or clear with g).\n"
        "\n"
        "In the popup: pick a font, Enter sends, Esc/q cancels.\n"
        "\n"
        "Key binding example: /key bind meta-t /tdf\n"
        "\n"
        "Options: /set plugins.var.python.tdf.*",
        "",
        "tdf_cmd_cb",
        "",
    )
