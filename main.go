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

	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

const version = "0.0.1"
const tabstop = 8

type Row struct {
	chars  []byte
	render []byte
}

func (r *Row) InsertChar(at, c int) {
	if at < 0 || at > len(r.chars) {
		at = len(r.chars)
	}
	r.chars = slices.Insert(r.chars, at, byte(c))
	r.Update()
}

func (r *Row) Update() {
	render := make([]byte, 0, len(r.chars))
	for _, b := range r.chars {
		if b == '\t' {
			render = append(render, ' ')
			for len(render)%tabstop != 0 {
				render = append(render, ' ')
			}
		} else {
			render = append(render, b)
		}
	}
	r.render = render
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
	rows       []Row
	status     string
	statustime time.Time
	filename   string
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
		row := Row{chars: slices.Clone(sc.Bytes())}
		row.Update()
		E.rows = append(E.rows, row)
		E.numrows++
	}
	if err := sc.Err(); err != nil {
		die("failed to read file: %s", err)
	}
}

func editorSave() {
	if E.filename == "" {
		editorSetStatus("no filename")
		return
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

func editorAppendRow(s []byte) {
	row := Row{
		chars: s,
	}
	row.Update()
	E.rows = append(E.rows, row)
	E.numrows++
}

func editorInsertChar(c int) {
	if E.cy == E.numrows {
		editorAppendRow(nil)
	}
	E.rows[E.cy].InsertChar(E.cx, c)
	E.cx++
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
			E.cx = len(E.rows[E.cy].chars)
		}
	case '\r':
		// TODO
	case DeleteKey, controlKey('h'), BackspaceKey:
		// TODO
	case controlKey('l'), '\x1b':
		// ignore
	default:
		editorInsertChar(c)
	}
}

func editorMoveCursor(c int) {
	var row Row
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
			E.cx = len(E.rows[E.cy].chars)
		}
	case ArrowRight:
		if row.chars != nil && E.cx < len(row.chars) {
			E.cx++
		} else if row.chars != nil && E.cx == len(row.chars) {
			E.cy++
			E.cx = 0
		}
	}

	if E.cy < E.numrows {
		row := E.rows[E.cy]
		if E.cx > len(row.chars) {
			E.cx = len(row.chars)
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
			line := E.rows[filerow].render
			coloff := E.coloff
			if coloff >= len(line) {
				coloff = 0
			}
			line = line[coloff:]
			if len(line) > E.screencols {
				line = line[:E.screencols]
			}
			b.Write(line)
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
	editorSetStatus("HELP: Ctrl-Q = quit")
	// byte reader loop
	for {
		editorRefreshScreen()
		editorProcessKeypress()
	}
}
