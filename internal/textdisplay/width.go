// Package textdisplay measures the terminal cells used by LIP input and
// diagnostics. Combining marks and joined emoji stay with their base character.
package textdisplay

import "unicode"

// ExpandTabs uses four-cell stops, matching the editor and source excerpts.
func ExpandTabs(text string) string {
	var expanded []rune
	column := 0
	runes := []rune(text)
	for i := 0; i < len(runes); {
		next := Next(runes, i)
		if runes[i] == '\t' {
			spaces := 4 - column%4
			for j := 0; j < spaces; j++ {
				expanded = append(expanded, ' ')
			}
			column += spaces
		} else {
			expanded = append(expanded, runes[i:next]...)
			column += Width(string(runes[i:next]))
		}
		i = next
	}
	return string(expanded)
}

func combining(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0xFE0F || r >= 0x1F3FB && r <= 0x1F3FF
}

func Next(text []rune, cursor int) int {
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

func Previous(text []rune, cursor int) int {
	previous := 0
	for i := 0; i < cursor; i = Next(text, i) {
		previous = i
	}
	return previous
}

func RuneWidth(r rune) int {
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

func Width(text string) int {
	runes, total := []rune(text), 0
	for i := 0; i < len(runes); {
		next, width := Next(runes, i), 0
		for _, r := range runes[i:next] {
			if r == 0xFE0F {
				width = 2
			}
			if w := RuneWidth(r); w > width {
				width = w
			}
		}
		total += width
		i = next
	}
	return total
}
