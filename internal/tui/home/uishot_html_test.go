package home

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// shotFrame is one captured screen, with a caption saying what state it holds.
type shotFrame struct {
	Name string
	Text string
}

// xterm's first sixteen, as a mainstream dark terminal renders them. The
// point is to be representative, not to match any one emulator exactly.
var base16 = [16]string{
	"#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
	"#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#f5f5f5",
}

const (
	defaultFG = "#d4d4d4"
	defaultBG = "#1e1e1e"
)

// xterm256 resolves a 256-color index: the sixteen base colors, then the
// 6×6×6 cube, then the greyscale ramp.
func xterm256(n int) string {
	switch {
	case n < 16:
		return base16[n]
	case n < 232:
		n -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", level(n/36), level((n%36)/6), level(n%6))
	default:
		v := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", v, v, v)
	}
}

// pen is the terminal's current graphic state.
type pen struct {
	fg, bg      string
	bold, faint bool
	reverse     bool
}

func (p *pen) apply(params []int) {
	for i := 0; i < len(params); i++ {
		switch code := params[i]; {
		case code == 0:
			*p = pen{}
		case code == 1:
			p.bold = true
		case code == 2:
			p.faint = true
		case code == 7:
			p.reverse = true
		case code == 22:
			p.bold, p.faint = false, false
		case code == 27:
			p.reverse = false
		case code == 39:
			p.fg = ""
		case code == 49:
			p.bg = ""
		case code >= 30 && code <= 37:
			p.fg = base16[code-30]
		case code >= 40 && code <= 47:
			p.bg = base16[code-40]
		case code >= 90 && code <= 97:
			p.fg = base16[code-90+8]
		case code >= 100 && code <= 107:
			p.bg = base16[code-100+8]
		case (code == 38 || code == 48) && i+2 < len(params) && params[i+1] == 5:
			if code == 38 {
				p.fg = xterm256(params[i+2])
			} else {
				p.bg = xterm256(params[i+2])
			}
			i += 2
		case (code == 38 || code == 48) && i+4 < len(params) && params[i+1] == 2:
			rgb := fmt.Sprintf("#%02x%02x%02x", params[i+2], params[i+3], params[i+4])
			if code == 38 {
				p.fg = rgb
			} else {
				p.bg = rgb
			}
			i += 4
		}
	}
}

func (p *pen) css() string {
	fg, bg := p.fg, p.bg
	if fg == "" {
		fg = defaultFG
	}
	if bg == "" {
		bg = defaultBG
	}
	if p.reverse {
		fg, bg = bg, fg
	}
	style := fmt.Sprintf("color:%s;background:%s", fg, bg)
	if p.bold {
		style += ";font-weight:700"
	}
	if p.faint {
		style += ";opacity:.55"
	}
	return style
}

var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// shotRender turns one frame's escape sequences into spans.
func shotRender(frame string) string {
	var b strings.Builder
	var p pen
	last := 0
	emit := func(text string) {
		if text != "" {
			fmt.Fprintf(&b, `<span style="%s">%s</span>`, p.css(), html.EscapeString(text))
		}
	}
	for _, match := range sgr.FindAllStringSubmatchIndex(frame, -1) {
		emit(frame[last:match[0]])
		params := []int{0}
		if raw := frame[match[2]:match[3]]; raw != "" {
			params = params[:0]
			for _, field := range strings.Split(raw, ";") {
				value, _ := strconv.Atoi(field)
				params = append(params, value)
			}
		}
		p.apply(params)
		last = match[1]
	}
	emit(frame[last:])
	return b.String()
}

// shotPage is a standalone HTML document holding every frame, captioned.
func shotPage(title string, frames []shotFrame) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><meta charset="utf-8"><title>%s</title><style>
body{margin:0;padding:24px;background:#111;color:#888;
     font:13px/1.5 -apple-system,system-ui,sans-serif}
h2{font:600 13px/1 inherit;margin:28px 0 8px;color:#aaa}
h2:first-of-type{margin-top:0}
pre{margin:0;padding:14px;background:%s;color:%s;display:inline-block;
    white-space:pre;font:14px/1.35 "SF Mono",Menlo,Consolas,monospace;
    border-radius:6px}
</style>`, html.EscapeString(title), defaultBG, defaultFG)
	for _, frame := range frames {
		fmt.Fprintf(&b, "<h2>%s</h2><pre>%s</pre>\n",
			html.EscapeString(frame.Name), shotRender(frame.Text))
	}
	return b.String()
}
