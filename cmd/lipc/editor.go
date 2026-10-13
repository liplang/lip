package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var errInputCanceled = errors.New("input canceled")

const editorHelp = "终端编辑：←/→ 移动，↑/↓ 历史；Ctrl-A/E 行首/行尾，Alt-B/F 按词移动；Ctrl-U/K/W 剪切，Ctrl-Y 粘回，Ctrl-_ 撤销，Ctrl-R 搜索，Tab 补全；Ctrl-C 丢弃输入。"

func commandCompletions(prefix string, names []string, commands string) []string {
	for _, command := range strings.Fields(commands) {
		if strings.HasPrefix(command, prefix) {
			names = append(names, command)
		}
	}
	return names
}

// One shared editor serves REPL and learn. Raw mode exists only during Read,
// and no reader goroutine consumes terminal input while a cell is running.
type lineEditor struct {
	input        *bufio.Reader
	output       io.Writer
	file         *os.File
	history      []string
	complete     func(string) []string
	row, columns int
	killed       string
}

type editState struct {
	text   string
	cursor int
}

func (e *lineEditor) readByte(ctx context.Context, timeout time.Duration) (byte, error) {
	start := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if e.input.Buffered() > 0 || terminalAvailable(e.file) {
			return e.input.ReadByte()
		}
		if timeout > 0 && time.Since(start) >= timeout {
			return 0, os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}

func (e *lineEditor) readRune(ctx context.Context, timeout time.Duration) (rune, error) {
	var bytes [utf8.UTFMax]byte
	for n := 0; n < len(bytes); n++ {
		b, err := e.readByte(ctx, timeout)
		if err != nil {
			return 0, err
		}
		bytes[n] = b
		if utf8.FullRune(bytes[:n+1]) {
			r, _ := utf8.DecodeRune(bytes[:n+1])
			return r, nil
		}
	}
	return utf8.RuneError, nil
}

func (e *lineEditor) escape(ctx context.Context) (string, error) {
	r, err := e.readRune(ctx, 50*time.Millisecond)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if r != '[' && r != 'O' {
		return "alt-" + string(r), nil
	}
	seq := string(r)
	for len(seq) < 32 {
		r, err = e.readRune(ctx, 50*time.Millisecond)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		seq += string(r)
		if r >= 0x40 && r <= 0x7e {
			return seq, nil
		}
	}
	return "", nil
}

func insertInput(text []rune, cursor int, added string) ([]rune, int) {
	result := append([]rune(nil), text[:cursor]...)
	result = append(result, []rune(added)...)
	position := len(result)
	result = append(result, text[cursor:]...)
	return result, position
}

func wordLeft(text []rune, cursor int) int {
	for cursor > 0 && unicode.IsSpace(text[cursor-1]) {
		cursor = clusterPrevious(text, cursor)
	}
	for cursor > 0 && !unicode.IsSpace(text[cursor-1]) {
		cursor = clusterPrevious(text, cursor)
	}
	return cursor
}
func wordRight(text []rune, cursor int) int {
	for cursor < len(text) && !unicode.IsSpace(text[cursor]) {
		cursor = clusterNext(text, cursor)
	}
	for cursor < len(text) && unicode.IsSpace(text[cursor]) {
		cursor = clusterNext(text, cursor)
	}
	return cursor
}

func (e *lineEditor) paste(ctx context.Context) (string, error) {
	var text strings.Builder
	for text.Len() < 4<<20 {
		r, err := e.readRune(ctx, 0)
		if err != nil {
			return "", err
		}
		text.WriteRune(r)
		if r == '~' && strings.HasSuffix(text.String(), "\x1b[201~") {
			result := strings.TrimSuffix(text.String(), "\x1b[201~")
			result = strings.ReplaceAll(strings.ReplaceAll(result, "\r\n", "\n"), "\r", "\n")
			result = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) && r != '\n' && r != '\t' {
					return -1
				}
				return r
			}, result)
			return strings.TrimRight(result, "\n"), nil
		}
	}
	return "", fmt.Errorf("paste exceeds 4 MiB")
}

func (e *lineEditor) readLine(ctx context.Context, prompt string) (string, error) {
	restore, err := rawTerminal(e.file)
	if err != nil {
		return "", err
	}
	defer restore()
	e.columns, e.row = terminalColumns(e.file), 0
	if e.columns < 2 {
		e.columns = 80
	}
	fmt.Fprint(e.output, "\x1b[?2004h")
	defer fmt.Fprint(e.output, "\x1b[?2004l")
	var text, draft []rune
	cursor, draftCursor, historyIndex := 0, 0, len(e.history)
	var undo []editState
	searching, searchIndex := false, len(e.history)
	var query []rune
	var searchOrigin editState
	find := func(from int) {
		for i := from - 1; i >= 0; i-- {
			if strings.Contains(e.history[i], string(query)) {
				text, cursor, searchIndex = []rune(e.history[i]), len([]rune(e.history[i])), i
				return
			}
		}
		fmt.Fprint(e.output, "\a")
	}
	draw := func() {
		shown := prompt
		if searching {
			shown = "(reverse-i-search) " + string(query) + ": "
		}
		e.redraw(shown, text, cursor)
	}
	draw()
	for {
		r, err := e.readRune(ctx, 0)
		if err != nil {
			e.finish(prompt, text)
			return "", err
		}
		before := editState{string(text), cursor}
		undone := false
		if searching {
			switch r {
			case 18:
				find(searchIndex)
				draw()
				continue
			case 7:
				text, cursor = []rune(searchOrigin.text), searchOrigin.cursor
				searching = false
				draw()
				continue
			case 8, 127:
				if len(query) > 0 {
					query = query[:clusterPrevious(query, len(query))]
				}
				text, cursor = []rune(searchOrigin.text), searchOrigin.cursor
				find(len(e.history))
				draw()
				continue
			case 3, '\r', '\n', 27, 1, 2, 4, 5, 6:
				searching = false
			default:
				if !unicode.IsControl(r) {
					query = append(query, r)
					find(len(e.history))
				}
				draw()
				continue
			}
		}
		switch r {
		case '\r', '\n':
			e.finish(prompt, text)
			result := string(text)
			if result != "" && (len(e.history) == 0 || e.history[len(e.history)-1] != result) {
				e.history = append(e.history, result)
				if len(e.history) > 500 {
					e.history = e.history[1:]
				}
			}
			return result + "\n", nil
		case 3:
			e.redraw(prompt, text, len(text))
			fmt.Fprint(e.output, "^C\r\n")
			return "", errInputCanceled
		case 4:
			if len(text) == 0 {
				e.finish(prompt, text)
				return "", io.EOF
			}
			if cursor < len(text) {
				text = append(text[:cursor], text[clusterNext(text, cursor):]...)
			}
		case 1:
			cursor = 0
		case 5:
			cursor = len(text)
		case 2:
			cursor = clusterPrevious(text, cursor)
		case 6:
			cursor = clusterNext(text, cursor)
		case 8, 127:
			if cursor > 0 {
				previous := clusterPrevious(text, cursor)
				text = append(text[:previous], text[cursor:]...)
				cursor = previous
			}
		case 11:
			e.killed = string(text[cursor:])
			text = text[:cursor]
		case 21:
			e.killed = string(text[:cursor])
			text, cursor = append([]rune(nil), text[cursor:]...), 0
		case 23:
			previous := wordLeft(text, cursor)
			e.killed = string(text[previous:cursor])
			text = append(text[:previous], text[cursor:]...)
			cursor = previous
		case 25:
			text, cursor = insertInput(text, cursor, e.killed)
		case 31:
			if len(undo) > 0 {
				state := undo[len(undo)-1]
				undo = undo[:len(undo)-1]
				text, cursor = []rune(state.text), state.cursor
				undone = true
			}
		case 20:
			if len(text) > 0 && cursor > 0 {
				right := cursor
				if right == len(text) {
					right = clusterPrevious(text, right)
				}
				if right > 0 {
					left, end := clusterPrevious(text, right), clusterNext(text, right)
					replacement := string(text[right:end]) + string(text[left:right])
					text, _ = insertInput(append(append([]rune(nil), text[:left]...), text[end:]...), left, replacement)
					cursor = end
				}
			}
		case 18:
			searching, searchIndex, query, searchOrigin = true, len(e.history), nil, before
			find(searchIndex)
		case 12:
			fmt.Fprint(e.output, "\x1b[2J\x1b[H")
			e.row = 0
		case '\t':
			start := cursor
			for start > 0 && completionRune(text[start-1]) {
				start--
			}
			prefix := string(text[start:cursor])
			if prefix != "" && e.complete != nil {
				candidates := e.complete(prefix)
				if len(candidates) > 0 {
					common := []rune(candidates[0])
					for _, candidate := range candidates[1:] {
						for !strings.HasPrefix(candidate, string(common)) {
							common = common[:len(common)-1]
						}
					}
					if len(common) > len([]rune(prefix)) {
						text, cursor = insertInput(text, cursor, strings.TrimPrefix(string(common), prefix))
					} else if len(candidates) > 1 {
						e.finish(prompt, text)
						fmt.Fprint(e.output, strings.Join(candidates, "  ")+"\r\n")
						e.row = 0
					}
				} else {
					fmt.Fprint(e.output, "\a")
				}
			}
		case 27:
			key, err := e.escape(ctx)
			if err != nil {
				return "", err
			}
			switch key {
			case "[D", "OD":
				cursor = clusterPrevious(text, cursor)
			case "[C", "OC":
				cursor = clusterNext(text, cursor)
			case "[H", "OH", "[1~", "[7~":
				cursor = 0
			case "[F", "OF", "[4~", "[8~":
				cursor = len(text)
			case "[1;5D", "[1;3D", "alt-b":
				cursor = wordLeft(text, cursor)
			case "[1;5C", "[1;3C", "alt-f":
				cursor = wordRight(text, cursor)
			case "alt-d":
				end := wordRight(text, cursor)
				e.killed = string(text[cursor:end])
				text = append(text[:cursor], text[end:]...)
			case "alt-\x7f":
				previous := wordLeft(text, cursor)
				e.killed = string(text[previous:cursor])
				text = append(text[:previous], text[cursor:]...)
				cursor = previous
			case "[3~":
				if cursor < len(text) {
					text = append(text[:cursor], text[clusterNext(text, cursor):]...)
				}
			case "[A", "OA", "[B", "OB":
				if historyIndex == len(e.history) {
					draft, draftCursor = append([]rune(nil), text...), cursor
				}
				if (key == "[A" || key == "OA") && historyIndex > 0 {
					historyIndex--
				}
				if (key == "[B" || key == "OB") && historyIndex < len(e.history) {
					historyIndex++
				}
				if historyIndex == len(e.history) {
					text, cursor = append([]rune(nil), draft...), draftCursor
				} else {
					text = []rune(e.history[historyIndex])
					cursor = len(text)
				}
			case "[200~":
				pasted, err := e.paste(ctx)
				if err != nil {
					return "", err
				}
				text, cursor = insertInput(text, cursor, pasted)
			}
		default:
			if !unicode.IsControl(r) {
				text, cursor = insertInput(text, cursor, string(r))
			}
		}
		if !undone && string(text) != before.text {
			undo = append(undo, before)
			if len(undo) > 100 {
				undo = undo[1:]
			}
		}
		draw()
	}
}

// Pipes retain plain line input, without prompts, raw mode or ANSI sequences.
type interactiveInput struct {
	editor *lineEditor
	lines  <-chan inputLine
}

func newInteractiveInput(ctx context.Context, complete func(string) []string) *interactiveInput {
	if terminalInput(os.Stdin) && terminalInput(os.Stdout) {
		return &interactiveInput{editor: &lineEditor{input: bufio.NewReader(os.Stdin), file: os.Stdin, output: os.Stdout, complete: complete}}
	}
	return &interactiveInput{lines: interactiveLines(ctx, os.Stdin)}
}
func (in *interactiveInput) read(ctx context.Context, prompt string) (string, error) {
	if in.editor != nil {
		return in.editor.readLine(ctx, prompt)
	}
	fmt.Fprint(os.Stdout, prompt)
	return nextLine(ctx, in.lines)
}

// Render explicit wraps instead of relying on the terminal's deferred wrap at
// the right margin. Cursor coordinates use display cells, not UTF-8 bytes.
func inputLayout(prompt string, text []rune, cursor, columns int) (display string, endRow, cursorRow, cursorColumn int) {
	var rendered strings.Builder
	row, column := 0, 0
	write := func(cluster string) {
		width := displayWidth(cluster)
		if column+width > columns {
			rendered.WriteString("\r\n")
			row++
			column = 0
		}
		rendered.WriteString(cluster)
		column += width
		if column >= columns {
			rendered.WriteString("\r\n")
			row++
			column = 0
		}
	}
	for _, r := range prompt {
		write(string(r))
	}
	for i := 0; i < len(text); {
		if i == cursor {
			cursorRow, cursorColumn = row, column
		}
		next := clusterNext(text, i)
		switch text[i] {
		case '\n':
			rendered.WriteString("\r\n")
			row++
			column = 0
			for _, r := range "   ...: " {
				write(string(r))
			}
		case '\t':
			spaces := 4 - column%4
			for j := 0; j < spaces; j++ {
				write(" ")
			}
		default:
			write(string(text[i:next]))
		}
		i = next
	}
	if cursor == len(text) {
		cursorRow, cursorColumn = row, column
	}
	return rendered.String(), row, cursorRow, cursorColumn
}

func (e *lineEditor) redraw(prompt string, text []rune, cursor int) {
	fmt.Fprint(e.output, "\r")
	if e.row > 0 {
		fmt.Fprintf(e.output, "\x1b[%dA", e.row)
	}
	fmt.Fprint(e.output, "\x1b[J")
	display, end, row, column := inputLayout(prompt, text, cursor, e.columns)
	fmt.Fprint(e.output, display, "\r")
	if end > row {
		fmt.Fprintf(e.output, "\x1b[%dA", end-row)
	}
	if column > 0 {
		fmt.Fprintf(e.output, "\x1b[%dC", column)
	}
	e.row = row
}

func (e *lineEditor) finish(prompt string, text []rune) {
	e.redraw(prompt, text, len(text))
	fmt.Fprint(e.output, "\r\n")
}

func completionRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == ':'
}

func combining(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0xFE0F || r >= 0x1F3FB && r <= 0x1F3FF
}

func clusterNext(text []rune, cursor int) int {
	if cursor >= len(text) {
		return len(text)
	}
	start := cursor
	cursor++
	if text[start] >= 0x1F1E6 && text[start] <= 0x1F1FF && cursor < len(text) && text[cursor] >= 0x1F1E6 && text[cursor] <= 0x1F1FF {
		cursor++
	}
	for cursor < len(text) {
		if combining(text[cursor]) {
			cursor++
			continue
		}
		if text[cursor] == 0x200D && cursor+1 < len(text) {
			cursor += 2
			continue
		}
		break
	}
	return cursor
}

func clusterPrevious(text []rune, cursor int) int {
	previous := 0
	for i := 0; i < cursor; i = clusterNext(text, i) {
		previous = i
	}
	return previous
}

func runeWidth(r rune) int {
	if combining(r) || r == 0x200D || unicode.IsControl(r) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115F || r == 0x2329 || r == 0x232A ||
		r >= 0x2E80 && r <= 0xA4CF || r >= 0xAC00 && r <= 0xD7A3 ||
		r >= 0xF900 && r <= 0xFAFF || r >= 0xFE10 && r <= 0xFE19 ||
		r >= 0xFE30 && r <= 0xFE6F || r >= 0xFF00 && r <= 0xFF60 ||
		r >= 0xFFE0 && r <= 0xFFE6 || r >= 0x1F000 && r <= 0x1FAFF || r >= 0x20000 && r <= 0x3FFFD) {
		return 2
	}
	return 1
}

func displayWidth(text string) int {
	runes, total := []rune(text), 0
	for i := 0; i < len(runes); {
		next, width := clusterNext(runes, i), 0
		for _, r := range runes[i:next] {
			if r == 0xFE0F {
				width = 2
			}
			if w := runeWidth(r); w > width {
				width = w
			}
		}
		total += width
		i = next
	}
	return total
}
