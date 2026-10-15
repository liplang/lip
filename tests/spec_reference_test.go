package tests

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
)

// Execute the algorithm printed in the spec, independently of script/values.go,
// so edits to the reference cannot silently disagree with its bit-vector table.
func TestSpecNumericReference(t *testing.T) {
	for _, edition := range []string{"LIP-1.0-SPEC.md", "LIP-1.0-SPEC.en.md"} {
		t.Run(edition, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", "./reference_numeric", "-spec", "../spec/"+edition, "-check")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("numeric reference: %v\n%s", err, output)
			}
			t.Logf("normative reference vectors:\n%s", output)
		})
	}
}

func TestDocumentationConsistency(t *testing.T) {
	root := ".."
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	fences := regexp.MustCompile("(?ms)^```([^\\n]*)\\n(.*?)^```[^\\n]*")
	prose := func(text string) string { return fences.ReplaceAllString(text, "") }
	headings := regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	links := regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	anchors := func(text string) map[string]bool {
		out := map[string]bool{}
		counts := map[string]int{}
		for _, match := range headings.FindAllStringSubmatch(prose(text), -1) {
			title := links.ReplaceAllString(match[1], "$1")
			var slug strings.Builder
			for _, r := range strings.ToLower(title) {
				if r == ' ' {
					slug.WriteByte('-')
				} else if r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) {
					slug.WriteRune(r)
				}
			}
			base := slug.String()
			name := base
			if counts[base] > 0 {
				name += fmt.Sprint("-", counts[base])
			}
			counts[base]++
			out[name] = true
		}
		return out
	}
	codes := regexp.MustCompile(`\bE-[A-Z][A-Z0-9-]*\b`)
	codeSet := func(text string) []string {
		set := map[string]bool{}
		for _, code := range codes.FindAllString(text, -1) {
			set[code] = true
		}
		var out []string
		for code := range set {
			out = append(out, code)
		}
		sort.Strings(out)
		return out
	}
	var documents []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
		} else if strings.HasSuffix(path, ".md") {
			documents = append(documents, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range documents {
		text := read(path)
		other := strings.TrimSuffix(path, ".md") + ".en.md"
		if strings.HasSuffix(path, ".en.md") {
			other = strings.TrimSuffix(path, ".en.md") + ".md"
		}
		companion := read(other)
		if !strings.Contains(prose(text), "]("+filepath.Base(other)+")") {
			t.Errorf("%s: missing language switch to %s", path, other)
		}
		if !reflect.DeepEqual(codeSet(text), codeSet(companion)) {
			t.Errorf("diagnostic coverage differs: %s / %s", path, other)
		}
		if !strings.HasSuffix(path, ".en.md") {
			original, translated := fences.FindAllStringSubmatch(text, -1), fences.FindAllStringSubmatch(companion, -1)
			if len(original) != len(translated) {
				t.Errorf("reference block count differs: %s (%d) / %s (%d)", path, len(original), other, len(translated))
			} else {
				for i, block := range original {
					if block[1] != translated[i][1] {
						t.Errorf("%s: reference block %d languages differ", path, i+1)
					}
					if !strings.ContainsFunc(block[2], func(r rune) bool { return unicode.Is(unicode.Han, r) }) && block[2] != translated[i][2] {
						t.Errorf("%s: reference block %d differs between editions", path, i+1)
					}
				}
			}
			destinations := func(text string) []string {
				set := map[string]bool{}
				for _, link := range links.FindAllStringSubmatch(prose(text), -1) {
					target := strings.SplitN(link[2], "#", 2)[0]
					set[strings.ReplaceAll(target, ".en.md", ".md")] = true
				}
				var names []string
				for name := range set {
					names = append(names, name)
				}
				sort.Strings(names)
				return names
			}
			if !reflect.DeepEqual(destinations(text), destinations(companion)) {
				t.Errorf("linked files/examples differ: %s / %s", path, other)
			}
		}
		for _, link := range links.FindAllStringSubmatch(prose(text), -1) {
			target := link[2]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			parts := strings.SplitN(target, "#", 2)
			dest := path
			if parts[0] != "" {
				dest = filepath.Join(filepath.Dir(path), parts[0])
			}
			if _, err := os.Stat(dest); err != nil {
				t.Errorf("%s: broken link %s: %v", path, target, err)
			} else if len(parts) == 2 && strings.HasSuffix(dest, ".md") && !anchors(read(dest))[parts[1]] {
				t.Errorf("%s: missing anchor %s", path, target)
			}
		}
	}
	zh := read(filepath.Join(root, "spec/LIP-1.0-SPEC.md"))
	en := read(filepath.Join(root, "spec/LIP-1.0-SPEC.en.md"))
	sections := regexp.MustCompile(`(?m)^#{2,3}\s+(\d+(?:\.\d+)?)\.`)
	sectionIDs := func(text string) []string {
		var ids []string
		for _, match := range sections.FindAllStringSubmatch(prose(text), -1) {
			ids = append(ids, match[1])
		}
		return ids
	}
	if !reflect.DeepEqual(sectionIDs(zh), sectionIDs(en)) {
		t.Error("specification section numbers differ between editions")
	}
	// Evidence tables must continue to point to tests that actually exist.
	testFiles, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var tests strings.Builder
	for _, path := range testFiles {
		tests.WriteString(read(path))
	}
	evidence := regexp.MustCompile("`([A-Z][A-Za-z0-9]+)(?:/[^`]+)?`")
	for _, path := range []string{"IMPLEMENTATION.md", "IMPLEMENTATION.en.md"} {
		for _, line := range strings.Split(read(filepath.Join(root, path)), "\n") {
			if !strings.HasPrefix(line, "| §") {
				continue
			}
			cells := strings.Split(line, "|")
			for _, match := range evidence.FindAllStringSubmatch(cells[len(cells)-2], -1) {
				if !strings.Contains(tests.String(), "func Test"+match[1]+"(") {
					t.Errorf("%s: missing evidence Test%s", path, match[1])
				}
			}
		}
	}
}
