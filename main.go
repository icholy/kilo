package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
	"unicode"

	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

const version = "0.0.1"
const tabstop = 8

type Highlight int

const (
	HighlightNormal Highlight = iota
	HighlightNumber
	HighlightMatch
)

func editorSyntaxToColor(hl Highlight) int {
	switch hl {
	case HighlightNumber:
		return 31
	case HighlightMatch:
		return 34
	default:
		return 37
	}
}

type Row struct {
	chars  []byte
	render []byte
	hl     []Highlight
}

func (r *Row) Len() int {
	return len(r.chars)
}

func (r *Row) Truncate(n int) {
	if r.Len() > n {
		r.chars = r.chars[:n]
		r.Update()
	}
}

func (r *Row) InsertChar(at, c int) {
	if at < 0 || at > r.Len() {
		at = r.Len()
	}
	r.chars = slices.Insert(r.chars, at, byte(c))
	r.Update()
}

func (r *Row) DeleteChar(at int) {
	if at < 0 || at > r.Len() {
		return
	}
	r.chars = slices.Delete(r.chars, at, at+1)
	r.Update()
}

func (r *Row) Append(chars []byte) {
	r.chars = append(r.chars, chars...)
	r.Update()
}

func (r *Row) Update() {
	if r.render == nil {
		r.render = make([]byte, 0, r.Len())
	} else {
		r.render = r.render[:0]
	}
	for _, b := range r.chars {
		if b == '\t' {
			r.render = append(r.render, ' ')
			for len(r.render)%tabstop != 0 {
				r.render = append(r.render, ' ')
			}
		} else {
			r.render = append(r.render, b)
		}
	}
	r.UpdateSyntax()
}

func (r *Row) UpdateSyntax() {
	if len(r.hl) < len(r.render) {
		r.hl = make([]Highlight, len(r.render))
	}
	for i, c := range r.render {
		if '0' <= c && c <= '9' {
			r.hl[i] = HighlightNumber
		} else {
			r.hl[i] = HighlightNormal
		}
	}
}

func (r Row) CxToRx(cx int) int {
	var rx int
	for _, c := range r.chars[:cx] {
		if c == '\t' {
			rx += (tabstop - 1) - rx%tabstop
		}
		rx++
	}
	return rx
}

var E struct {
	termios    unix.Termios
	screenrows int
	screencols int
	cx         int
	cy         int
	rx         int
	numrows    int
	rowoff     int
	coloff     int
	rows       []*Row
	debug      string
	status     string
	statustime time.Time
	filename   string
	dirty      bool
}

func enableRawMode() {
	raw, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err != nil {
		log.Fatalf("failed to get termios: %v", err)
	}
	E.termios = *raw
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag &^= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
	if err := unix.IoctlSetTermios(unix.Stdin, unix.TCSETS, raw); err != nil {
		log.Fatalf("failed to set termios: %v", err)
	}
}

func restoreMode() {
	if err := unix.IoctlSetTermios(unix.Stdin, unix.TCSETS, &E.termios); err != nil {
		log.Fatalf("failed to restore termios: %v", err)
	}
}

func die(format string, args ...any) {
	editorRefreshScreen()
	msg := fmt.Sprintf(format, args...)
	unix.Write(unix.Stdout, []byte(msg))
	unix.Exit(0)
}

func initEditor() {
	E.screenrows, E.screencols = getWindowSize()
	E.screenrows -= 2 // room for status bar & message
}

func editorOpen(filename string) {
	E.filename = filename
	f, err := os.Open(filename)
	if err != nil {
		die("failed to open file: %s", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		editorInsertRow(E.numrows, slices.Clone(sc.Bytes()))
	}
	if err := sc.Err(); err != nil {
		die("failed to read file: %s", err)
	}
}

func editorSave() {
	if E.filename == "" {
		name, ok := editorPrompt("Save as:", nil)
		if !ok {
			return
		}
		E.filename = name
	}
	f, err := os.OpenFile(E.filename, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		die("save failed: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(0); err != nil {
		die("save failed: %v", err)
	}
	if err := writeRowsTo(f); err != nil {
		die("save failed: %v", err)
	}
	if err := f.Close(); err != nil {
		die("save failed: %v", err)
	}
	E.dirty = false
	editorSetStatus("saved %s", E.filename)
}

func getWindowSize() (rows, cols int) {
	ws, err := unix.IoctlGetWinsize(unix.Stdout, unix.TIOCGWINSZ)
	if err != nil {
		// fallback mechanism
		if _, err := unix.Write(unix.Stdout, []byte("\x1b[999C\x1b[999B")); err != nil {
			die("failed to get window size: %v", err)
		}
		return getCursorPosition()

	}
	return int(ws.Row), int(ws.Col)
}

func getCursorPosition() (row, col int) {
	if _, err := unix.Write(unix.Stdout, []byte("\x1b[6n")); err != nil {
		die("getCursorPosition: %v", err)
	}
	var buf [32]byte
	var i int
	for i < len(buf)-1 {
		if n, _ := unix.Read(unix.Stdin, buf[i:i+1]); n != 1 {
			break
		}
		if buf[i] == 'R' {
			break
		}
		i++
	}
	if buf[0] != '\x1b' || buf[1] != '[' {
		die("invalid escape sequence")
	}
	if n, err := fmt.Sscanf(string(buf[2:i]), "%d;%d", &row, &col); n != 2 {
		die("failed to scan cursor pos: %v", err)
	}
	return row, col
}

func controlKey(c byte) int {
	return int(c & 0b00011111)
}

const (
	BackspaceKey = 127
	ArrowLeft    = iota + 1000
	ArrowRight
	ArrowUp
	ArrowDown
	PageUp
	PageDown
	HomeKey
	EndKey
	DeleteKey
)

func editorReadKey() int {
	var c int
	var b [1]byte
	for {
		n, err := unix.Read(unix.Stdin, b[:])
		if n == 1 {
			c = int(b[0])
			break
		}
		if n == -1 && err != unix.EAGAIN {
			die("read: %v", err)
		}
	}
	// handle escale sequences
	if c == '\x1b' {
		var seq [3]byte
		if n, _ := unix.Read(unix.Stdin, seq[:1]); n != 1 {
			return c
		}
		if n, _ := unix.Read(unix.Stdin, seq[1:2]); n != 1 {
			return c
		}
		if seq[0] == '[' {
			// page up/page down
			if seq[1] >= '0' && seq[1] <= '9' {
				if n, _ := unix.Read(unix.Stdin, seq[2:]); n != 1 {
					return c
				}
				if seq[2] == '~' {
					switch seq[1] {
					case '3':
						return DeleteKey
					case '5':
						return PageUp
					case '6':
						return PageDown
					case '1', '7':
						return HomeKey
					case '4', '8':
						return EndKey
					}
				}
			}
			// arrow keys
			switch seq[1] {
			case 'A':
				return ArrowUp
			case 'B':
				return ArrowDown
			case 'C':
				return ArrowRight
			case 'D':
				return ArrowLeft
			case 'H':
				return HomeKey
			case 'F':
				return EndKey
			}
		} else {
			if seq[0] == 'O' {
				switch seq[1] {
				case 'H':
					return HomeKey
				case 'F':
					return EndKey
				}
			}
		}
	}
	return c
}

func editorPrompt(prompt string, callback func(input string, key int)) (string, bool) {
	var input []byte
	for {
		editorSetStatus("%s %s (ESC to cancel)", prompt, input)
		editorRefreshScreen()
		c := editorReadKey()
		if c == DeleteKey || c == controlKey('h') || c == BackspaceKey {
			if len(input) > 0 {
				input = input[:len(input)-1]
			}
		} else if c == '\x1b' || c == controlKey('q') {
			editorSetStatus("")
			return "", false
		} else if c == '\r' {
			if len(input) != 0 {
				editorSetStatus("")
				if callback != nil {
					callback(string(input), c)
				}
				return string(input), true
			}
		} else if unicode.IsPrint(rune(c)) && c < 128 {
			input = append(input, byte(c))
		}
		if callback != nil {
			callback(string(input), c)
		}
	}
}

type SearchMatch struct {
	cx, cy int
}

func editorFind() {
	// save the cursor state in case we cancel
	cx, cy := E.cx, E.cy
	rowoff, coloff := E.rowoff, E.coloff

	// the search matches
	var matchidx int
	var matches []SearchMatch

	_, ok := editorPrompt("Search:", func(input string, c int) {
		switch c {
		case '\r', '\x1b':
			return
		case ArrowUp, ArrowLeft:
			matchidx--
		case ArrowDown, ArrowRight:
			matchidx++
		default:
			if len(input) == 0 {
				return
			}
			matches = matches[:0]
			query := []byte(input)
			for y, r := range E.rows {
				r.UpdateSyntax() // clear highlight
				var off int
				for off < len(r.chars) {
					i := bytes.Index(r.chars[off:], query)
					if i < 0 {
						break
					}
					m := SearchMatch{cx: off + i, cy: y}
					matches = append(matches, m)
					off += i + 1

					// highlight
					rx := r.CxToRx(m.cx)
					for x := rx; x < rx+len(query); x++ {
						r.hl[x] = HighlightMatch
					}
				}
			}
		}

		if len(matches) > 0 {
			// fix the match index
			if matchidx < 0 {
				matchidx += len(matches)
			} else {
				matchidx = matchidx % len(matches)
			}
			m := matches[matchidx]
			E.cy = m.cy
			E.cx = m.cx
			E.rowoff = E.numrows
		}
	})
	// restore cursor if user hit escape
	if !ok {
		E.cx = cx
		E.cy = cy
		E.rowoff = rowoff
		E.coloff = coloff
	}
	// clear the status line
	E.debug = ""
	// clear highlights
	for _, r := range E.rows {
		r.UpdateSyntax()
	}
}

func editorSetStatus(format string, args ...any) {
	E.status = fmt.Sprintf(format, args...)
	E.statustime = time.Now()
}

func editorDrawStatusBar(b *bytes.Buffer) {
	// status bar
	b.WriteString("\x1b[7m")
	filename := E.filename
	if filename == "" {
		filename = "[No Name]"
	}
	status := fmt.Sprintf("%.20s - line %d/%d", filename, E.cy+1, E.numrows)
	if E.dirty {
		status += " (modified)"
	}
	if E.debug != "" {
		status += " " + E.debug
	}
	if len(status) > E.screencols {
		status = status[:E.screencols]
	}
	b.WriteString(status)
	for i := len(status); i < E.screencols; i++ {
		b.WriteString(" ")
	}
	b.WriteString("\x1b[m")
	b.WriteString("\r\n")
	// status message
	b.WriteString("\x1b[K")
	if E.status != "" {
		if time.Since(E.statustime) > 5*time.Second {
			E.status = ""
			return
		}
		message := E.status
		if len(status) > E.screencols {
			message = message[:E.screencols]
		}
		b.WriteString(message)
	}
}

func editorInsertRow(at int, chars []byte) {
	row := &Row{chars: chars}
	row.Update()
	E.rows = slices.Insert(E.rows, at, row)
	E.numrows++
	E.dirty = true
}

func editorDeleteRow(at int) {
	if at < 0 || at >= E.numrows {
		return
	}
	if E.cx == 0 && E.cy == 0 {
		return
	}
	E.rows = slices.Delete(E.rows, at, at+1)
	E.numrows--
	E.dirty = true
}

func editorInsertChar(c int) {
	if E.cy == E.numrows {
		editorInsertRow(E.numrows, nil)
	}
	E.rows[E.cy].InsertChar(E.cx, c)
	E.cx++
	E.dirty = true
}

func editorDeleteChar() {
	if E.cy == E.numrows {
		return
	}
	if E.cx == 0 && E.cy == 0 {
		return
	}
	row := E.rows[E.cy]
	if E.cx > 0 {
		row.DeleteChar(E.cx - 1)
		E.cx--
	} else {
		E.cx = E.rows[E.cy-1].Len()
		E.rows[E.cy-1].Append(row.chars)
		editorDeleteRow(E.cy)
		E.cy--
	}
}

func editorInsertNewline() {
	if E.cx == 0 {
		editorInsertRow(E.cy, nil)
	} else {
		editorInsertRow(E.cy+1, E.rows[E.cy].chars[E.cx:])
		E.rows[E.cy].Truncate(E.cx)
	}
	E.cy++
	E.cx = 0
}

func editorProcessKeypress() {
	c := editorReadKey()
	switch c {
	case controlKey('q'):
		editorRefreshScreen()
		restoreMode()
		unix.Exit(0)
	case controlKey('s'):
		editorSave()
	case controlKey('f'):
		editorFind()
	case ArrowUp, ArrowDown, ArrowLeft, ArrowRight:
		editorMoveCursor(c)
	case PageUp:
		E.cy = E.rowoff
		for i := 0; i < E.screenrows; i++ {
			editorMoveCursor(ArrowUp)
		}
	case PageDown:
		E.cy = E.rowoff + E.screenrows - 1
		if E.cy > E.numrows {
			E.cy = E.numrows
		}
		for i := 0; i < E.screenrows; i++ {
			editorMoveCursor(ArrowDown)
		}
	case HomeKey:
		E.cx = 0
	case EndKey:
		if E.cy < E.numrows {
			E.cx = E.rows[E.cy].Len()
		}
	case '\r':
		editorInsertNewline()
	case DeleteKey:
		editorMoveCursor(ArrowRight)
		editorDeleteChar()
	case controlKey('h'), BackspaceKey:
		editorDeleteChar()
	case controlKey('l'), '\x1b':
		// ignore
	default:
		editorInsertChar(c)
	}
}

func editorMoveCursor(c int) {
	var row *Row
	if E.cy < E.numrows {
		row = E.rows[E.cy]
	}
	switch c {
	case ArrowUp:
		if E.cy > 0 {
			E.cy--
		}
	case ArrowDown:
		if E.cy < E.numrows {
			E.cy++
		}
	case ArrowLeft:
		if E.cx > 0 {
			E.cx--
		} else if E.cy > 0 {
			E.cy--
			E.cx = E.rows[E.cy].Len()
		}
	case ArrowRight:
		if row.chars != nil && E.cx < row.Len() {
			E.cx++
		} else if row.chars != nil && E.cx == row.Len() {
			E.cy++
			E.cx = 0
		}
	}

	if E.cy < E.numrows {
		row := E.rows[E.cy]
		if E.cx > row.Len() {
			E.cx = row.Len()
		}
	}
}

func writeRowsTo(w io.Writer) error {
	for _, r := range E.rows {
		if _, err := w.Write(r.chars); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}

func editorScroll() {
	E.rx = 0
	if E.cy < E.numrows {
		E.rx = E.rows[E.cy].CxToRx(E.cx)
	}
	if E.cy < E.rowoff {
		E.rowoff = E.cy
	}
	if E.cy >= E.rowoff+E.screenrows {
		E.rowoff = E.cy - E.screenrows + 1
	}
	if E.rx < E.coloff {
		E.coloff = E.rx
	}
	if E.rx >= E.coloff+E.screencols {
		E.coloff = E.rx - E.screencols + 1
	}
}

func editorRefreshScreen() {
	editorScroll()
	var b bytes.Buffer
	b.WriteString("\x1b[?25l") // hide cursor
	b.WriteString("\x1b[H")    // put cursor at top left
	editorDrawRows(&b)
	editorDrawStatusBar(&b)
	fmt.Fprintf(&b, "\x1b[%d;%dH", E.cy-E.rowoff+1, E.rx-E.coloff+1) // move cursor to correct position
	b.WriteString("\x1b[?25h")                                       // show cursor
	unix.Write(unix.Stdout, b.Bytes())
}

func editorDrawRows(b *bytes.Buffer) {
	for y := 0; y < E.screenrows; y++ {
		filerow := y + E.rowoff
		if filerow >= E.numrows {
			// print welcome screen
			if E.numrows == 0 && y == E.screenrows/3 {
				welcome := fmt.Sprintf("Kilo editor -- version %s", version)
				if len(welcome) > E.screencols {
					welcome = welcome[:E.screencols]
				}
				padding := (E.screencols - len(welcome)) / 2
				b.WriteString(strings.Repeat(" ", padding))
				b.WriteString(welcome)
			} else {
				b.WriteString("~")
			}
		} else {
			row := E.rows[filerow]
			line := row.render
			coloff := E.coloff
			if coloff >= len(line) {
				coloff = 0
			}
			line = line[coloff:]
			if len(line) > E.screencols {
				line = line[:E.screencols]
			}
			var prevcolor int
			for i, c := range line {
				hl := row.hl[i+coloff]
				if hl == HighlightNormal {
					b.WriteString("\x1b[39m")
					prevcolor = -1
				} else {
					if color := editorSyntaxToColor(hl); color != prevcolor {
						fmt.Fprintf(b, "\x1b[%dm", color)
						prevcolor = color
					}
				}
				b.WriteByte(c)
			}
			b.WriteString("\x1b[39m")
		}
		b.WriteString("\x1b[K") // clear one line
		b.WriteString("\r\n")
	}
}

func main() {
	flag.Parse()
	// raw mode
	enableRawMode()
	defer restoreMode()
	// setup
	initEditor()
	if flag.NArg() > 0 {
		editorOpen(flag.Arg(0))
	}
	// show help message
	editorSetStatus("HELP: Ctrl-S = save | Ctrl-Q = quit | Ctrl-F = find")
	// byte reader loop
	for {
		editorRefreshScreen()
		editorProcessKeypress()
	}
}
