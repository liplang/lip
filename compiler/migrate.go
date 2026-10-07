package compiler

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
	"lipalpha/compiler/token"
)

type sourceEdit struct {
	start, end int
	text       string
}

// MigrateSource updates only explicit syntax. Omitted parameter types become
// any; output types are inferred when possible. Missing inputs, bindings or
// returns are never invented. Comments and formatting are preserved.
func MigrateSource(source string) (string, []string, error) {
	tokens, err := lexer.New(source).Lex()
	if err != nil {
		return "", nil, err
	}
	var edits []sourceEdit
	sourceRunes := []rune(source)
	var report []string
	for i, t := range tokens {
		if t.Kind == token.Ident && (t.Text == "requires" || t.Text == "import") && i+2 < len(tokens) && tokens[i+1].Kind == token.Ident && tokens[i+2].Kind == token.String {
			edits = append(edits, sourceEdit{t.Pos.Offset, t.Pos.Offset + len([]rune(t.Text)), "require"})
			report = append(report, fmt.Sprintf("%d:%d: replace %s with require", t.Pos.Line, t.Pos.Column, t.Text))
			tokens[i].Kind, tokens[i].Text = token.Require, "require"
		}
	}
	program, err := parser.NewLegacy(tokens).Parse()
	if err != nil {
		return "", nil, err
	}
	for _, fn := range program.Functions {
		for _, name := range fn.Params {
			if fn.ParamTypes[name] == "" {
				fn.ParamTypes[name] = "any"
			}
		}
	}
	graph, err := build(program, true)
	if err != nil {
		return "", nil, err
	}
	for i, t := range tokens {
		if t.Kind != token.Fn && t.Kind != token.Flow {
			continue
		}
		j := i + 2
		if tokens[j].Kind != token.LParen {
			end := tokens[i+1].Pos.Offset + len([]rune(tokens[i+1].Text))
			edits = append(edits, sourceEdit{end, end, "()"})
			report = append(report, fmt.Sprintf("%d:%d: add explicit empty parameter list", t.Pos.Line, t.Pos.Column))
		} else {
			j++
			for tokens[j].Kind != token.RParen {
				param := tokens[j]
				j++
				if tokens[j].Kind == token.Colon {
					j += 2
				} else {
					end := param.Pos.Offset + len([]rune(param.Text))
					edits = append(edits, sourceEdit{end, end, ": any"})
					report = append(report, fmt.Sprintf("%d:%d: annotate %s as any; refine to a concrete type when known", param.Pos.Line, param.Pos.Column, param.Text))
				}
				if tokens[j].Kind == token.Comma {
					j++
				}
			}
			j++
		}
		if t.Kind == token.Flow && tokens[j].Kind != token.Arrow {
			at := tokens[j].Pos.Offset
			prefix := ""
			if at > 0 && !unicode.IsSpace(sourceRunes[at-1]) {
				prefix = " "
			}
			edits = append(edits, sourceEdit{at, at, prefix + "-> " + graph.ReturnType + " "})
			report = append(report, fmt.Sprintf("%d:%d: declare Flow output as %s", t.Pos.Line, t.Pos.Column, graph.ReturnType))
		}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	runes := sourceRunes
	for start := 0; start < len(edits); {
		end := start + 1
		for end < len(edits) && edits[end].start == edits[start].start {
			end++
		}
		// Insertions sharing an offset must appear in their original order.
		for index := end - 1; index >= start; index-- {
			edit := edits[index]
			runes = append(append(append([]rune(nil), runes[:edit.start]...), []rune(edit.text)...), runes[edit.end:]...)
		}
		start = end
	}
	result := string(runes)
	if _, err := ParseAndBuild(result); err != nil {
		return "", nil, fmt.Errorf("migrated source: %w", err)
	}
	if len(report) == 0 {
		report = []string{"source already satisfies the Alpha 0.5 contract"}
	}
	return result, report, nil
}

func MigrationReport(report []string) string { return strings.Join(report, "\n") }
