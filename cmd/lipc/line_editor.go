package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"lipalpha/internal/listops"
	"lipalpha/internal/stringops"
	"lipalpha/internal/textdisplay"
)

var errInputCanceled = errors.New("input canceled")

// Raw mode is scoped to one read, so compiled cells and child processes run
// with the user's original terminal settings.
type lineEditor struct {
	input    *bufio.Reader
	output   io.Writer
	file     *os.File
	history  []string
	complete func(string) []string
	row      int
	columns  int
}

func (e *lineEditor) escapeRune() (rune, bool, error) {
	if e.file != nil && e.input.Buffered() == 0 {
		deadline := time.Now().Add(50 * time.Millisecond)
		for !terminalAvailable(e.file) {
			if time.Now().After(deadline) {
				return 0, false, nil
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	r, _, err := e.input.ReadRune()
	return r, err == nil, err
}

func (e *lineEditor) escape() (string, error) {
	r, ok, err := e.escapeRune()
	if err != nil || !ok {
		return "", err
	}
	if r != '[' && r != 'O' {
		return "alt-" + string(r), nil
	}
	sequence := string(r)
	for len(sequence) < 24 {
		r, ok, err = e.escapeRune()
		if err != nil || !ok {
			return "", err
		}
		sequence += string(r)
		if r >= 0x40 && r <= 0x7E {
			return sequence, nil
		}
	}
	return "", nil
}

func insertInput(text []rune, cursor int, added string) ([]rune, int) {
	addition := []rune(added)
	result := make([]rune, 0, len(text)+len(addition))
	result = append(result, text[:cursor]...)
	result = append(result, addition...)
	result = append(result, text[cursor:]...)
	return result, cursor + len(addition)
}

func wordLeft(text []rune, cursor int) int {
	for cursor > 0 && unicode.IsSpace(text[cursor-1]) {
		cursor = textdisplay.Previous(text, cursor)
	}
	for cursor > 0 && !unicode.IsSpace(text[cursor-1]) {
		cursor = textdisplay.Previous(text, cursor)
	}
	return cursor
}

func wordRight(text []rune, cursor int) int {
	for cursor < len(text) && !unicode.IsSpace(text[cursor]) {
		cursor = textdisplay.Next(text, cursor)
	}
	for cursor < len(text) && unicode.IsSpace(text[cursor]) {
		cursor = textdisplay.Next(text, cursor)
	}
	return cursor
}

func (e *lineEditor) paste() (string, error) {
	var pasted strings.Builder
	for {
		r, _, err := e.input.ReadRune()
		if err != nil {
			return "", err
		}
		pasted.WriteRune(r)
		if r == '~' && strings.HasSuffix(pasted.String(), "\x1b[201~") {
			break
		}
	}
	text := strings.TrimSuffix(pasted.String(), "\x1b[201~")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	// Terminal control bytes in pasted text must never become editing commands.
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
	return strings.TrimRight(text, "\n"), nil
}

func (e *lineEditor) readLine(prompt string) (string, error) {
	if e.file != nil {
		restore, err := rawTerminal(e.file)
		if err != nil {
			return "", fmt.Errorf("cannot edit terminal input: %w", err)
		}
		defer restore()
		e.columns = terminalColumns(e.file)
	}
	if e.columns < 2 {
		e.columns = 80
	}
	e.row = 0
	fmt.Fprint(e.output, "\x1b[?2004h")
	defer fmt.Fprint(e.output, "\x1b[?2004l")
	var text []rune
	cursor, historyIndex := 0, len(e.history)
	var draft []rune
	draftCursor := 0
	e.redraw(prompt, text, cursor)
	for {
		r, _, err := e.input.ReadRune()
		if err != nil {
			e.finish(prompt, text)
			if err == io.EOF && len(text) > 0 {
				return string(text) + "\n", io.EOF
			}
			return "", err
		}
		switch r {
		case '\r', '\n':
			e.finish(prompt, text)
			return string(text) + "\n", nil
		case 3: // Ctrl-C discards the entire pending cell.
			e.redraw(prompt, text, len(text))
			fmt.Fprint(e.output, "^C\r\n")
			return "", errInputCanceled
		case 4:
			if len(text) == 0 {
				e.finish(prompt, text)
				return "", io.EOF
			}
			if cursor < len(text) {
				text = append(text[:cursor], text[textdisplay.Next(text, cursor):]...)
			}
		case 1:
			cursor = 0
		case 5:
			cursor = len(text)
		case 2:
			cursor = textdisplay.Previous(text, cursor)
		case 6:
			cursor = textdisplay.Next(text, cursor)
		case 8, 127:
			if cursor > 0 {
				previous := textdisplay.Previous(text, cursor)
				text = append(text[:previous], text[cursor:]...)
				cursor = previous
			}
		case 11:
			text = text[:cursor]
		case 21:
			text, cursor = append([]rune(nil), text[cursor:]...), 0
		case 23:
			previous := wordLeft(text, cursor)
			text = append(text[:previous], text[cursor:]...)
			cursor = previous
		case 12:
			fmt.Fprint(e.output, "\x1b[2J\x1b[H")
			e.row = 0
		case '\t':
			start := cursor
			for start > 0 && completionRune(text[start-1]) {
				start--
			}
			prefix := string(text[start:cursor])
			if e.complete == nil || prefix == "" {
				break
			}
			candidates := e.complete(prefix)
			if len(candidates) == 0 {
				fmt.Fprint(e.output, "\a")
				break
			}
			common := candidates[0]
			for _, candidate := range candidates[1:] {
				for !strings.HasPrefix(candidate, common) {
					common = string([]rune(common)[:len([]rune(common))-1])
				}
			}
			if len(common) > len(prefix) {
				text, cursor = insertInput(text, cursor, strings.TrimPrefix(common, prefix))
			} else if len(candidates) > 1 {
				e.finish(prompt, text)
				fmt.Fprintln(e.output, strings.Join(candidates, "  "))
				e.row = 0
			}
		case 27:
			key, err := e.escape()
			if err != nil && err != io.EOF {
				return "", err
			}
			switch key {
			case "[D", "OD":
				cursor = textdisplay.Previous(text, cursor)
			case "[C", "OC":
				cursor = textdisplay.Next(text, cursor)
			case "[H", "OH", "[1~", "[7~":
				cursor = 0
			case "[F", "OF", "[4~", "[8~":
				cursor = len(text)
			case "[1;5D", "alt-b":
				cursor = wordLeft(text, cursor)
			case "[1;5C", "alt-f":
				cursor = wordRight(text, cursor)
			case "[3~":
				if cursor < len(text) {
					text = append(text[:cursor], text[textdisplay.Next(text, cursor):]...)
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
				pasted, err := e.paste()
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
		e.redraw(prompt, text, cursor)
	}
}

// Render explicit wraps instead of relying on the terminal's deferred wrap at
// the right margin. Cursor coordinates use display cells, not UTF-8 bytes.
func inputLayout(prompt string, text []rune, cursor, columns int) (display string, endRow, cursorRow, cursorColumn int) {
	var rendered strings.Builder
	row, column := 0, 0
	write := func(cluster string) {
		width := textdisplay.Width(cluster)
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
		next := textdisplay.Next(text, i)
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

func (s *replSession) completions(prefix string) []string {
	names := map[string]bool{}
	for _, word := range strings.Fields("flow fn return when for in if else import as true false null any number bool string list object void str len range fold fail print state retry feedback :help :vars :history :reset :cancel :quit :exit") {
		names[word] = true
	}
	for name := range s.values {
		names[name] = true
	}
	for _, fn := range s.functions {
		names[fn.Name] = true
	}
	for _, dependency := range s.dependencies {
		if dependency.Kind != "python" {
			continue
		}
		name := dependency.Alias
		if name == "" {
			name = strings.FieldsFunc(dependency.Spec, func(r rune) bool { return strings.ContainsRune("<>=!~ ", r) })[0]
		}
		names[name+"."] = true
	}
	for _, operation := range listops.All() {
		names[operation.Name] = true
	}
	for _, operation := range stringops.All() {
		names[operation.Name] = true
	}
	var matches []string
	for name := range names {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	return matches
}
