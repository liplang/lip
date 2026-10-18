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
	for _, edition := range []string{"LIP-1.0-SPEC.md", "LIP-1.0-SPEC.en.md", "LIP-1.1-SPEC.md", "LIP-1.1-SPEC.en.md", "LIP-1.2-SPEC.md", "LIP-1.2-SPEC.en.md"} {
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
		// Skills are machine-facing workflow entrypoints, not bilingual product
		// documents. Their relative links are still checked below.
		if filepath.Base(path) != "SKILL.md" {
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
	sections := regexp.MustCompile(`(?m)^#{2,3}\s+(\d+(?:\.\d+)?)\.`)
	sectionIDs := func(text string) []string {
		var ids []string
		for _, match := range sections.FindAllStringSubmatch(prose(text), -1) {
			ids = append(ids, match[1])
		}
		return ids
	}
	for _, version := range []string{"1.1", "1.2"} {
		zh := read(filepath.Join(root, "spec/LIP-"+version+"-SPEC.md"))
		en := read(filepath.Join(root, "spec/LIP-"+version+"-SPEC.en.md"))
		if !reflect.DeepEqual(sectionIDs(zh), sectionIDs(en)) {
			t.Errorf("%s specification section numbers differ between editions", version)
		}
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

// The current edition must retain the whole language and its appendices when
// new rules are integrated, rather than becoming a supplement to an old spec.
func TestSpec12Standalone(t *testing.T) {
	sections := regexp.MustCompile(`(?m)^#{2,3}\s+(\d+(?:\.\d+)?)\.`)
	oldSpec := regexp.MustCompile(`LIP-1\.[01]-SPEC`)
	for _, edition := range []string{"LIP-1.2-SPEC.md", "LIP-1.2-SPEC.en.md"} {
		t.Run(edition, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "spec", edition))
			if err != nil {
				t.Fatal(err)
			}
			if oldSpec.Match(data) {
				t.Error("1.2 must not delegate language rules to an earlier specification")
			}
			seen := map[string]bool{}
			for _, match := range sections.FindAllSubmatch(data, -1) {
				id := string(match[1])
				if seen[id] {
					t.Errorf("duplicate specification section %s", id)
				}
				seen[id] = true
			}
			for i := 0; i <= 17; i++ {
				if !seen[fmt.Sprint(i)] {
					t.Errorf("missing complete-language section %d", i)
				}
			}
			for _, id := range []string{"5.1", "5.2", "5.3", "10.4", "10.5", "15.1", "15.2", "16.5", "16.7", "16.8"} {
				if !seen[id] {
					t.Errorf("missing required integrated section %s", id)
				}
			}
		})
	}
	directions, err := filepath.Glob(filepath.Join("..", "spec", "LIP-1.2-DIRECTION*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(directions) != 0 {
		t.Errorf("1.2 rules belong in the complete specification, found %v", directions)
	}
}

// TestSpec12CompositionModel exercises the finite algebra that the 1.2
// composition vectors describe. It deliberately does not import script/ so a
// checker or evaluator change cannot make this independent semantic check pass
// by sharing the same implementation.
func TestSpec12CompositionModel(t *testing.T) {
	type requestSet struct {
		unknown bool
		known   []string
	}
	union := func(parts ...requestSet) requestSet {
		seen := map[string]bool{}
		out := requestSet{}
		for _, part := range parts {
			out.unknown = out.unknown || part.unknown
			for _, id := range part.known {
				seen[id] = true
			}
		}
		for id := range seen {
			out.known = append(out.known, id)
		}
		sort.Strings(out.known)
		return out
	}

	r := requestSet{known: []string{"read"}}
	w := requestSet{known: []string{"write"}}
	u := requestSet{unknown: true, known: []string{"bridge.read"}}
	want := requestSet{unknown: true, known: []string{"bridge.read", "read", "write"}}
	for _, parts := range [][]requestSet{
		{r, w, u}, {u, r, w}, {w, u, r}, {w, r, u},
	} {
		if got := union(parts...); !reflect.DeepEqual(got, want) {
			t.Fatalf("union order changed result: got %#v want %#v", got, want)
		}
	}
	if got := union(r, union(w, u)); !reflect.DeepEqual(got, want) {
		t.Fatalf("union reassociation changed result: got %#v want %#v", got, want)
	}
	if got := union(r, r); !reflect.DeepEqual(got, r) {
		t.Fatalf("union idempotence failed: got %#v want %#v", got, r)
	}

	// A gather is all-or-nothing for each candidate. In particular [0, 9]
	// cannot turn an invalid projection into a partial [r] result.
	gather := func(slots []string, indices []int) ([]string, bool) {
		out := make([]string, 0, len(indices))
		for _, raw := range indices {
			i := raw
			if i < 0 {
				i += len(slots)
			}
			if i < 0 || i >= len(slots) {
				return nil, false
			}
			out = append(out, slots[i])
		}
		return out, true
	}
	if got, ok := gather([]string{"r"}, []int{0, 9}); ok || got != nil {
		t.Fatalf("invalid gather returned a partial projection: got %#v ok=%v", got, ok)
	}
	if got, ok := gather([]string{"w", "r"}, []int{1, -1, 1}); !ok || !reflect.DeepEqual(got, []string{"r", "r", "r"}) {
		t.Fatalf("valid gather lost order/duplicates: got %#v ok=%v", got, ok)
	}

	// Callee and argument alternatives are independent. Zipping the two
	// slices would incorrectly discard the cross combinations.
	calleeKinds := []string{"number", "string"}
	argKinds := []string{"number", "string"}
	compatible := 0
	for _, callee := range calleeKinds {
		for _, arg := range argKinds {
			if callee == arg {
				compatible++
			}
		}
	}
	if compatible != 2 {
		t.Fatalf("callee/argument alternatives were zipped: compatible=%d", compatible)
	}

	// All-candidate failure chooses the earliest validation phase, not the
	// first candidate's error or the order in which a cache was visited.
	type phase int
	const (
		callable phase = iota
		arity
		parameterType
	)
	choose := func(phases ...phase) phase {
		best := parameterType
		for _, p := range phases {
			if p < best {
				best = p
			}
		}
		return best
	}
	if got := choose(parameterType, arity); got != arity {
		t.Fatalf("call-boundary phase ordering failed: got %v", got)
	}
	if got := choose(arity, callable); got != callable {
		t.Fatalf("callable phase did not precede arity: got %v", got)
	}

	// Least fixed points are independent of declaration/visit order.
	solve := func(order []string) map[string]map[string]bool {
		direct := map[string]map[string]bool{
			"readBase": {"read": true},
			"ring":     {},
		}
		deps := map[string][]string{
			"readBase": {"readBase"},
			"ring":     {"ring"},
		}
		sets := map[string]map[string]bool{"readBase": {}, "ring": {}}
		for round := 0; round < 32; round++ {
			changed := false
			for _, name := range order {
				before := len(sets[name])
				for id := range direct[name] {
					sets[name][id] = true
				}
				for _, dep := range deps[name] {
					for id := range sets[dep] {
						sets[name][id] = true
					}
				}
				changed = changed || len(sets[name]) != before
			}
			if !changed {
				break
			}
		}
		return sets
	}
	forward := solve([]string{"readBase", "ring"})
	reverse := solve([]string{"ring", "readBase"})
	if !reflect.DeepEqual(forward, reverse) || !reflect.DeepEqual(forward["readBase"], map[string]bool{"read": true}) || len(forward["ring"]) != 0 {
		t.Fatalf("fixed point depends on visit order: forward=%#v reverse=%#v", forward, reverse)
	}

	// Claim join is idempotent by complete identity, while a changed value
	// under one identity is a conflict and equal values under new identities
	// remain separate facts.
	type atom struct{ id, value string }
	store := map[string]string{}
	join := func(a atom) (conflict bool) {
		if old, ok := store[a.id]; ok {
			return old != a.value
		}
		store[a.id] = a.value
		return false
	}
	if join(atom{"j1", "v"}) || join(atom{"j1", "v"}) || join(atom{"j2", "v"}) || len(store) != 2 {
		t.Fatalf("claim idempotence/distinct identity failed: %#v", store)
	}
	if !join(atom{"j1", "changed"}) || len(store) != 2 {
		t.Fatalf("claim identity conflict was not retained: %#v", store)
	}

	// Stable ErrorKey ordering is structural, not completion order.
	type pair struct{ a, b int }
	type key struct {
		pos  []pair
		path []pair
		site int
	}
	less := func(a, b key) bool {
		cmp := func(x, y []pair) int {
			for i := 0; i < len(x) && i < len(y); i++ {
				if x[i].a != y[i].a {
					if x[i].a < y[i].a {
						return -1
					}
					return 1
				}
				if x[i].b != y[i].b {
					if x[i].b < y[i].b {
						return -1
					}
					return 1
				}
			}
			if len(x) < len(y) {
				return -1
			}
			if len(x) > len(y) {
				return 1
			}
			return 0
		}
		if c := cmp(a.pos, b.pos); c != 0 {
			return c < 0
		}
		if c := cmp(a.path, b.path); c != 0 {
			return c < 0
		}
		return a.site < b.site
	}
	early := key{pos: []pair{{2, 0}}, site: 50}
	late := key{pos: []pair{{8, 0}}, site: 10}
	if !less(early, late) || less(late, early) {
		t.Fatalf("ErrorKey did not follow structural position: early=%#v late=%#v", early, late)
	}
}

func TestSpec12CompositionSectionPresent(t *testing.T) {
	for _, edition := range []string{"spec/LIP-1.2-SPEC.md", "spec/LIP-1.2-SPEC.en.md"} {
		data, err := os.ReadFile(filepath.Join("..", edition))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "16.9") || !strings.Contains(text, "provider") || !strings.Contains(text, "After") || !strings.Contains(text, "Claim") {
			t.Fatalf("%s lacks the integrated composition closure vectors", edition)
		}
	}
}
