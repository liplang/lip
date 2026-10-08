package tests

import (
	"bytes"
	"encoding/json"
	"go/format"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"lipalpha/compiler"
	"lipalpha/internal/listops"
	"lipalpha/internal/stringops"
)

func documentationFiles(t *testing.T) []string {
	t.Helper()
	files := []string{"README.md", "README.en.md", "RELEASE.md", "CHANGELOG.md"}
	for _, dir := range []string{"docs", "examples"} {
		if err := filepath.WalkDir(filepath.Join("..", dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && filepath.Ext(path) == ".md" {
				files = append(files, strings.TrimPrefix(filepath.ToSlash(path), "../"))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// Every LIP fence is checked, including top-level programs and library snippets.
// Expressions and exercise templates declare how to supply their small wrapper;
// deliberately invalid programs must name the expected diagnostic, never skip.
func TestDocumentationPrograms(t *testing.T) {
	fences := regexp.MustCompile("(?m)^```(lip|json)\n([\\s\\S]*?)\n```")
	directive := regexp.MustCompile(`<!-- (lip-check|lip-diagnostic): ([^>]+) -->\s*$`)
	checked := 0
	for _, path := range documentationFiles(t) {
		data, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		var previous *compiler.CheckReport
		for _, match := range fences.FindAllSubmatchIndex(data, -1) {
			language, source := string(data[match[2]:match[3]]), string(data[match[4]:match[5]])
			marker := directive.FindSubmatch(data[:match[0]])
			line := bytes.Count(data[:match[0]], []byte("\n")) + 1
			if language == "json" {
				if marker == nil || string(marker[1]) != "lip-diagnostic" {
					continue
				}
				if string(marker[2]) != "previous" || previous == nil || previous.OK {
					t.Fatalf("%s:%d: diagnostic example needs a preceding checked failure", path, line)
				}
				var want compiler.CheckReport
				if err := json.Unmarshal([]byte(source), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(*previous, want) {
					t.Errorf("%s:%d: diagnostic JSON differs from the actual report\n got %+v\nwant %+v", path, line, *previous, want)
				}
				continue
			}
			mode := "program"
			if marker != nil {
				if string(marker[1]) != "lip-check" {
					t.Fatalf("%s:%d: incorrect LIP fence directive", path, line)
				}
				mode = string(marker[2])
			}
			switch mode {
			case "expression":
				source = "flow Example() -> any { return " + source + "\n}"
			case "template":
				if !strings.Contains(source, "__ANSWER__") {
					t.Fatalf("%s:%d: template has no placeholder", path, line)
				}
				source = strings.ReplaceAll(source, "__ANSWER__", "2 + 3 * 4")
			case "program":
			default:
				if !strings.HasPrefix(mode, "LIP_") {
					t.Fatalf("%s:%d: unknown check mode %q", path, line, mode)
				}
			}
			report := compiler.CheckSource("bad.lip", source)
			previous = &report
			checked++
			if strings.HasPrefix(mode, "LIP_") {
				if report.OK || len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != mode {
					t.Errorf("%s:%d: want %s, got %+v", path, line, mode, report)
				}
				continue
			}
			if !report.OK {
				t.Errorf("%s:%d: %+v", path, line, report.Diagnostics)
				continue
			}
			graph, err := compiler.ParseAndBuild(source)
			if err != nil {
				t.Fatal(err)
			}
			code, err := compiler.GenerateGo(graph)
			if err != nil {
				t.Fatalf("%s:%d: %v", path, line, err)
			}
			if _, err := format.Source([]byte(code)); err != nil {
				t.Errorf("%s:%d: generated Go: %v", path, line, err)
			}
		}
	}
	t.Logf("checked %d documentation LIP blocks", checked)
}

func TestDocumentedLibraryCatalogs(t *testing.T) {
	for _, library := range []struct {
		path, prefix string
		names        []string
	}{
		{"docs/LIST-LIBRARY.md", "list", func() []string {
			var names []string
			for _, spec := range listops.All() {
				names = append(names, spec.Name)
			}
			return names
		}()},
		{"docs/STRING-LIBRARY.md", "string", func() []string {
			var names []string
			for _, spec := range stringops.All() {
				names = append(names, spec.Name)
			}
			return names
		}()},
	} {
		data, err := os.ReadFile(filepath.Join("..", library.path))
		if err != nil {
			t.Fatal(err)
		}
		documented := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "| `") {
				for _, name := range regexp.MustCompile(library.prefix+`\.[a-z_]+`).FindAllString(strings.Split(line, " | ")[0], -1) {
					documented[name] = true
				}
			}
		}
		// parse_number has a prose signature instead of a table row.
		if library.prefix == "string" && strings.Contains(string(data), "`string.parse_number(s: string) -> number`") {
			documented["string.parse_number"] = true
		}
		for _, name := range library.names {
			if !documented[name] {
				t.Errorf("%s lacks the contract for %s", library.path, name)
			}
			delete(documented, name)
		}
		for name := range documented {
			t.Errorf("%s documents an unavailable operation: %s", library.path, name)
		}
	}
}

// Discover every generated example, so adding a new convenience output also
// adds it to the drift check. Most standalone outputs follow name/main.go.
func TestGeneratedExamplesMatchSources(t *testing.T) {
	libraries := map[string]string{
		"examples/python/flow_gen.go":               "examples/python/flow.lip",
		"examples/host_adapter/flow/flow_gen.go":    "examples/host_adapter/flow.lip",
		"examples/tutorial/mixedflow/flow_gen.go":   "examples/tutorial/mixed.lip",
		"examples/tutorial/sessionflow/flow_gen.go": "examples/tutorial/session.lip",
	}
	packageName := regexp.MustCompile(`(?m)^package (\w+)$`)
	count := 0
	err := filepath.WalkDir("../examples", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(data, []byte("// Code generated by lipc; DO NOT EDIT.")) {
			return nil
		}
		count++
		relative := strings.TrimPrefix(filepath.ToSlash(path), "../")
		source, library := libraries[relative]
		if !library {
			if filepath.Base(path) != "main.go" {
				t.Fatalf("add source mapping for %s", relative)
			}
			dir := filepath.Dir(relative)
			source = filepath.ToSlash(filepath.Join(filepath.Dir(dir), filepath.Base(dir)+".lip"))
		}
		graph, err := compiler.CompileFile(filepath.Join("..", source))
		if err != nil {
			return err
		}
		graph.SourceName = source
		pkg := packageName.FindSubmatch(data)
		if pkg == nil {
			t.Fatalf("%s has no package declaration", relative)
		}
		code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: string(pkg[1]), IncludeMain: !library})
		if err != nil {
			return err
		}
		formatted, err := format.Source([]byte(code))
		if err != nil {
			return err
		}
		if !bytes.Equal(data, formatted) {
			t.Errorf("regenerate %s from %s", relative, source)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("checked %d generated example files", count)
}

func TestDocumentationLocalLinks(t *testing.T) {
	fences := regexp.MustCompile("(?m)^```[\\s\\S]*?^```[^\\n]*$")
	links := regexp.MustCompile(`\]\(([^\s)]+)\)`)
	headings := regexp.MustCompile(`(?m)^#{1,6} (.+)$`)
	for _, path := range documentationFiles(t) {
		data, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		data = fences.ReplaceAll(data, nil)
		for _, link := range links.FindAllSubmatch(data, -1) {
			target := string(link[1])
			if strings.Contains(target, ":") {
				continue
			}
			relative, anchor, _ := strings.Cut(target, "#")
			relative, err := url.PathUnescape(relative)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(filepath.Dir(path), relative)
			if relative == "" {
				destination = path
			}
			info, err := os.Stat(filepath.Join("..", destination))
			if err != nil {
				t.Errorf("%s: broken link %s: %v", path, target, err)
				continue
			}
			if anchor == "" || info.IsDir() || filepath.Ext(destination) != ".md" {
				continue
			}
			anchor, err = url.PathUnescape(anchor)
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(filepath.Join("..", destination))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, heading := range headings.FindAllSubmatch(fences.ReplaceAll(contents, nil), -1) {
				// GitHub's heading IDs preserve letters and numbers (including
				// Chinese), remove punctuation, and replace spaces with dashes.
				slug := strings.Map(func(r rune) rune {
					if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || r == '-' || r == '_' {
						return unicode.ToLower(r)
					}
					return -1
				}, string(heading[1]))
				if strings.ReplaceAll(slug, " ", "-") == anchor {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: %s has no heading #%s", path, destination, anchor)
			}
		}
	}
}
