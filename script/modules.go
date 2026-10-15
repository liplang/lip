package script

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SourceSet is a frozen build input. Text is copied and normalized during
// compilation; no runtime path reads source files or resolves imports.
type Source struct{ Version, Text string }
type SourceSet struct {
	Root    string
	Sources map[string]Source
	Lock    *Value
}

const maxSourceBytes = 64 << 20
const maxSourceFiles = 4096

func validSourceID(id string) bool {
	if id == "" || !utf8.ValidString(id) || strings.ContainsAny(id, "\\\x00") || strings.HasPrefix(id, "/") {
		return false
	}
	for _, part := range strings.Split(id, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func moduleTarget(importer, relative string) (string, error) {
	if relative == "" || !utf8.ValidString(relative) || strings.ContainsAny(relative, "\\\x00") || path.IsAbs(relative) || path.Ext(relative) != ".lip" {
		return "", diagnostic("E-MODULE", "invalid LIP import path")
	}
	target := path.Clean(path.Join(path.Dir(importer), relative))
	if !validSourceID(target) {
		return "", diagnostic("E-MODULE", "LIP import escapes the project root")
	}
	return target, nil
}
func sourceHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// LoadSourceSet reads the complete local closure once. projectRoot fixes the
// identity boundary; an empty root uses the entry file's physical directory.
// Every path component below that root must match exactly and be nonsymlink.
func LoadSourceSet(filename, projectRoot string) (SourceSet, error) {
	return loadSourceSet(filename, projectRoot, nil, nil, nil)
}

// Interactive roots supply parsed syntax and reuse already frozen dependencies.
// Only newly imported files are read, before checking or executing the cell.
func loadSourceSet(filename, projectRoot string, supplied *syntax, frozen map[string]Source, physical map[string]os.FileInfo) (SourceSet, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return SourceSet{}, err
	}
	if projectRoot == "" {
		projectRoot = filepath.Dir(absolute)
	}
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		return SourceSet{}, err
	}
	relative, err := filepath.Rel(projectRoot, absolute)
	if err != nil {
		return SourceSet{}, diagnostic("E-MODULE", err.Error())
	}
	projectRoot, err = filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return SourceSet{}, diagnostic("E-MODULE", err.Error())
	}
	if !validSourceID(filepath.ToSlash(relative)) {
		// Callers may already use the physical filename with a logical root.
		relative, err = filepath.Rel(projectRoot, absolute)
		if err != nil {
			return SourceSet{}, diagnostic("E-MODULE", err.Error())
		}
	}
	set := SourceSet{Root: filepath.ToSlash(relative), Sources: map[string]Source{}}
	if !validSourceID(set.Root) {
		return SourceSet{}, diagnostic("E-MODULE", "root source is outside the project")
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return SourceSet{}, diagnostic("E-MODULE", err.Error())
	}
	defer root.Close()
	if physical == nil {
		physical = map[string]os.FileInfo{}
	}
	size := 0
	for id, source := range frozen {
		set.Sources[id] = source
		size += len(source.Text)
	}
	if supplied != nil {
		set.Sources[set.Root] = Source{Version: "local", Text: supplied.source}
		size += len(supplied.source)
	}
	if size > maxSourceBytes || len(set.Sources) > maxSourceFiles {
		return SourceSet{}, diagnostic("E-BUDGET", "source budget exhausted")
	}
	visited := map[string]bool{}
	var load func(string) error
	load = func(id string) error {
		if visited[id] {
			return nil
		}
		visited[id] = true
		if _, frozen := set.Sources[id]; !frozen {
			if len(set.Sources) >= maxSourceFiles {
				return diagnostic("E-BUDGET", "source file budget exhausted")
			}
			current := "."
			var checked os.FileInfo
			for _, part := range strings.Split(id, "/") {
				entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(current))
				if err != nil {
					return diagnostic("E-MODULE", err.Error())
				}
				exact := false
				for _, entry := range entries {
					if entry.Name() == part {
						exact = true
						break
					}
				}
				if !exact {
					return diagnostic("E-MODULE", "source path does not match exactly: "+id)
				}
				current = filepath.Join(current, part)
				info, err := root.Lstat(current)
				if err != nil {
					return diagnostic("E-MODULE", err.Error())
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return diagnostic("E-MODULE", "symlink in source path: "+id)
				}
				checked = info
			}
			if checked == nil || !checked.Mode().IsRegular() {
				return diagnostic("E-MODULE", "source must be a regular file: "+id)
			}
			file, err := root.Open(current)
			if err != nil {
				return diagnostic("E-MODULE", err.Error())
			}
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() || !os.SameFile(checked, info) {
				file.Close()
				return diagnostic("E-MODULE", "source must be a regular file: "+id)
			}
			for old, prior := range physical {
				if os.SameFile(info, prior) {
					file.Close()
					return diagnostic("E-MODULE", "duplicate physical source: "+old+" and "+id)
				}
			}
			if info.Size() > int64(maxSourceBytes-size) {
				file.Close()
				return diagnostic("E-BUDGET", "source byte budget exhausted")
			}
			data, err := io.ReadAll(io.LimitReader(file, int64(maxSourceBytes-size)+1))
			if closeErr := file.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return diagnostic("E-MODULE", err.Error())
			}
			size += len(data)
			if size > maxSourceBytes {
				return diagnostic("E-BUDGET", "source byte budget exhausted")
			}
			physical[id] = info
			set.Sources[id] = Source{Version: "local", Text: string(data)}
		}
		syntax := supplied
		var err error
		if id != set.Root || supplied == nil {
			syntax, err = parse(set.Sources[id].Text)
		}
		if err != nil {
			if d, ok := err.(*Diagnostic); ok {
				d.SourceID = id
			}
			return err
		}
		for _, im := range syntax.imports {
			if im.language == "lip" {
				target, err := moduleTarget(id, im.path)
				if err != nil {
					return err
				}
				if err := load(target); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := load(set.Root); err != nil {
		return SourceSet{}, err
	}
	return set, nil
}

func CompileSourceSet(set SourceSet, host Host, options CompileOptions) (program *Program, err error) {
	defer func() { err = diagnosticPhase(err, "static") }()
	syntax, build, err := linkSources(set, options.Entry, host.Native != nil)
	if err != nil {
		return nil, err
	}
	return compileSyntax(syntax, host, build)
}

// Linking changes internal names only. Tokens keep their original source and
// global ordinal, and lexical bindings never fall back to module declarations.
func linkSources(set SourceSet, entry string, nativeHelpers bool) (*syntax, Value, error) {
	return linkSourceSyntax(set, entry, nativeHelpers, nil, nil)
}

func linkSourceSyntax(set SourceSet, entry string, nativeHelpers bool, supplied *syntax, saved map[string]bool) (*syntax, Value, error) {
	if !validSourceID(set.Root) || len(set.Sources) == 0 || len(set.Sources) > maxSourceFiles {
		return nil, Value{}, diagnostic("E-MODULE", "invalid source set")
	}
	ids := make([]string, 0, len(set.Sources))
	for id := range set.Sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rootSource, ok := set.Sources[set.Root]
	if !ok {
		return nil, Value{}, diagnostic("E-MODULE", "root source is missing")
	}
	if len(rootSource.Text) > 2*maxSourceBytes {
		return nil, Value{}, diagnostic("E-BUDGET", "source byte budget exhausted")
	}
	rootText := strings.ReplaceAll(rootSource.Text, "\r\n", "\n")
	if len(rootText) > maxSourceBytes {
		return nil, Value{}, diagnostic("E-BUDGET", "source byte budget exhausted")
	}
	rootTokens, err := lex(rootText)
	if err != nil {
		if d, ok := err.(*Diagnostic); ok {
			d.SourceID = set.Root
		}
		return nil, Value{}, err
	}
	modules := map[string]*syntax{}
	qualified := map[string]map[string]string{}
	edges := map[string]map[string]string{}
	table := []Value{}
	// SourceTable sorts every SourceID. StaticSite has a distinct ordering:
	// root first, followed by the other sources in SourceID order.
	offset, size := len(rootTokens)-1, 0
	for i, id := range ids {
		source := set.Sources[id]
		if !validSourceID(id) || source.Version == "" || !utf8.ValidString(source.Version) {
			return nil, Value{}, diagnostic("E-MODULE", "source identity and version must be valid")
		}
		text := strings.ReplaceAll(source.Text, "\r\n", "\n")
		size += len(text)
		if size > maxSourceBytes {
			return nil, Value{}, diagnostic("E-BUDGET", "source byte budget exhausted")
		}
		s := supplied
		var err error
		if id != set.Root || supplied == nil {
			s, err = parse(text)
		}
		if err != nil {
			if d, ok := err.(*Diagnostic); ok {
				d.SourceID = id
			}
			return nil, Value{}, err
		}
		if id != set.Root && s.body != nil && len(s.body.items) > 0 {
			return nil, Value{}, diagnostic("E-MODULE", "dependency modules cannot contain top-level statements")
		}
		tokens, err := lex(text)
		if err != nil {
			return nil, Value{}, err
		}
		sourceOffset := offset
		if id == set.Root {
			sourceOffset = 0
		}
		mapSyntaxTokens(s, func(t *token) {
			if t.site >= 0 {
				t.site += sourceOffset
			}
			t.source = id
		})
		if id != set.Root {
			offset += len(tokens) - 1
		}
		modules[id] = s
		edges[id] = map[string]string{}
		qualified[id] = map[string]string{}
		prefix := ""
		if id != set.Root {
			prefix = "$m" + strconv.Itoa(i) + "_"
			if supplied != nil {
				// Stable names preserve captured module closures as imports grow.
				prefix = "$m" + sourceHash(id) + "_"
			}
		}
		for name := range s.functions {
			qualified[id][name] = prefix + name
		}
		for name := range s.flows {
			qualified[id][name] = prefix + name
		}
		for name := range s.data {
			qualified[id][name] = prefix + name
		}
		for name := range qualified[id] {
			if reserved(name) {
				return nil, Value{}, diagnostic("E-RESERVED", "reserved declaration "+name)
			}
		}
		aliases := map[string]bool{}
		for _, im := range s.imports {
			if reserved(im.alias) || qualified[id][im.alias] != "" {
				return nil, Value{}, at(diagnostic("E-RESERVED", "import alias conflicts with declaration"), im.tok)
			}
			if aliases[im.alias] {
				return nil, Value{}, at(diagnostic("E-DUPLICATE", "duplicate import alias"), im.tok)
			}
			aliases[im.alias] = true
			if im.language == "lip" {
				target, err := moduleTarget(id, im.path)
				if err != nil {
					return nil, Value{}, err
				}
				edges[id][im.path] = target
			}
		}
		table = append(table, ListValue(textValue(id), textValue(source.Version), textValue(text)))
	}
	root := modules[set.Root]
	if root == nil {
		return nil, Value{}, diagnostic("E-MODULE", "root source is missing")
	}
	if supplied == nil {
		if err := root.selectEntry(entry); err != nil {
			return nil, Value{}, err
		}
	}
	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if active[id] {
			return diagnostic("E-MODULE-CYCLE", "cyclic LIP imports")
		}
		if visited[id] {
			return nil
		}
		if modules[id] == nil {
			return diagnostic("E-MODULE", "missing imported source: "+id)
		}
		active[id] = true
		for _, target := range edges[id] {
			if err := visit(target); err != nil {
				return err
			}
		}
		delete(active, id)
		visited[id] = true
		return nil
	}
	if err := visit(set.Root); err != nil {
		return nil, Value{}, err
	}
	if len(visited) != len(modules) {
		return nil, Value{}, diagnostic("E-MODULE", "source set contains sources outside the root closure")
	}
	lockRows := [][]string{}
	for importer, paths := range edges {
		for relative, target := range paths {
			source := set.Sources[target]
			lockRows = append(lockRows, []string{importer, relative, target, source.Version, sourceHash(strings.ReplaceAll(source.Text, "\r\n", "\n"))})
		}
	}
	sort.Slice(lockRows, func(i, j int) bool {
		for k := 0; k < 4; k++ {
			if lockRows[i][k] != lockRows[j][k] {
				return lockRows[i][k] < lockRows[j][k]
			}
		}
		return false
	})
	lockValues := []Value{}
	for _, row := range lockRows {
		fields := []Value{}
		for _, value := range row {
			fields = append(fields, textValue(value))
		}
		lockValues = append(lockValues, ListValue(fields...))
	}
	sourceTable, lock := ListValue(table...), ListValue(lockValues...)
	if set.Lock != nil {
		same, err := Equal(*set.Lock, lock)
		if err != nil || !same {
			return nil, Value{}, diagnostic("E-MODULE", "dependency lock does not match frozen sources")
		}
	}
	revision := sourceHash(root.source)
	if len(modules) > 1 {
		revision = digest(ListValue(textValue("lip-source-set"), textValue("1.0"), textValue(set.Root), sourceTable, lock))
	}
	build := object(map[string]Value{"root": textValue(set.Root), "entry": textValue(root.flowName), "revision": textValue(revision), "source_table": sourceTable, "dependency_lock": lock})
	imports := []importDecl{}
	functions := map[string]*expression{}
	flows := map[string]*flowDecl{}
	data := map[string]dataDecl{}
	for i, id := range ids {
		s := modules[id]
		aliases, native := map[string]string{}, map[string]string{}
		for _, im := range s.imports {
			if im.language == "lip" {
				aliases[im.alias] = edges[id][im.path]
			} else {
				old := im.alias
				if id != set.Root {
					im.alias = "$i" + strconv.Itoa(i) + "_" + old
					if supplied != nil {
						im.alias = "$i" + sourceHash(id) + "_" + old
					}
				}
				native[old] = im.alias
				imports = append(imports, im)
			}
		}
		r := moduleRenamer{names: qualified[id], aliases: aliases, native: native, modules: modules, qualified: qualified, dependency: id != set.Root, nativeHelpers: nativeHelpers}
		bound := map[string]bool{}
		if id == set.Root {
			bound = copyBound(saved)
		}
		for _, f := range s.functions {
			r.expr(f, copyBound(bound))
		}
		for _, f := range s.flows {
			local := copyBound(bound)
			for _, param := range f.params {
				r.bind(local, param.name, param.tok)
			}
			r.block(f.body, local)
		}
		if (len(s.flows) == 0 || s.cell) && s.body != nil {
			r.block(s.body, copyBound(bound))
		}
		if r.err != nil {
			return nil, Value{}, r.err
		}
		for name, f := range s.functions {
			functions[qualified[id][name]] = f
		}
		for name, f := range s.flows {
			f.name = qualified[id][name]
			flows[f.name] = f
		}
		for name, d := range s.data {
			data[qualified[id][name]] = d
		}
	}
	if !root.cell && len(root.flows) == 0 && len(flows) > 0 {
		root.flowName = "$entry"
		flows[root.flowName] = &flowDecl{name: root.flowName, result: root.result, body: root.body}
	}
	root.imports, root.functions, root.flows, root.data = imports, functions, flows, data
	return root, build, nil
}

type moduleRenamer struct {
	names, native, aliases map[string]string
	stores                 map[string]bool
	qualified              map[string]map[string]string
	modules                map[string]*syntax
	err                    error
	dependency             bool
	nativeHelpers          bool
}

func (r *moduleRenamer) bind(bound map[string]bool, name string, t token) {
	if r.stores[name] {
		r.err = at(diagnostic("E-SHADOW-CLAIM", "Claim name cannot be shadowed"), t)
	}
	if r.native[name] != "" || r.aliases[name] != "" {
		r.err = at(diagnostic("E-RESERVED", "import alias cannot be rebound"), t)
	}
	bound[name] = true
}
func copyBound(bound map[string]bool) map[string]bool {
	out := map[string]bool{}
	for name, value := range bound {
		out[name] = value
	}
	return out
}
func (r *moduleRenamer) expr(e *expression, bound map[string]bool) {
	if e == nil || r.err != nil {
		return
	}
	if e.tag == "call" && e.x.tag == "id" && len(e.args) == 1 && r.nativeHelpers {
		name := e.x.name
		if (name == "native_data" || name == "native_release") && !bound[name] && r.names[name] == "" && r.native[name] == "" && r.aliases[name] == "" {
			// Mark the defining module's Host prelude before flattening names.
			// A declaration in the importer must not capture this helper call.
			e.x.name = "$" + name
			for _, arg := range e.args {
				r.expr(arg.expr, bound)
			}
			return
		}
	}
	if e.tag == "field" && e.x != nil && e.x.tag == "id" && !bound[e.x.name] {
		if target := r.aliases[e.x.name]; target != "" {
			name := r.qualified[target][e.name]
			if name == "" {
				r.err = at(diagnostic("E-NAME", "unknown module member "+e.name), e.x.tok)
				return
			}
			if !r.modules[target].exports[e.name] {
				r.err = at(diagnostic("E-EXPORT", "private module member "+e.name), e.x.tok)
				return
			}
			e.tag, e.name, e.tok, e.x = "id", name, e.x.tok, nil
			return
		}
	}
	if e.tag == "id" && !bound[e.name] {
		if name := r.names[e.name]; name != "" {
			e.name = name
		} else if alias := r.native[e.name]; alias != "" {
			e.name = alias
		} else if r.aliases[e.name] != "" {
			r.err = at(diagnostic("E-TYPE", "module alias is not a value"), e.tok)
		} else if _, builtin := builtinArity[e.name]; !builtin && r.dependency {
			// An unresolved dependency name must never fall through to a root
			// declaration in the linked checker environment.
			r.err = at(diagnostic("E-NAME", "unbound name "+e.name), e.tok)
		}
		return
	}
	if e.tag == "proposal" {
		if e.name == "claim.snapshot" && len(e.args) > 0 {
			arg := e.args[0].expr
			if arg.tag == "lit" && arg.value.k == String && !bound[arg.value.s] {
				if name := r.names[arg.value.s]; name != "" {
					arg.value = textValue(name)
				}
			}
		}
		parts := strings.SplitN(e.name, ".", 2)
		if len(parts) == 2 {
			if alias := r.native[parts[0]]; alias != "" && !bound[parts[0]] {
				e.name = alias + "." + parts[1]
			} else if name := r.names[parts[0]]; name != "" && !bound[parts[0]] {
				e.name = name + "." + parts[1]
			} else if r.dependency && e.name != "claim.snapshot" && !(r.nativeHelpers && bound[parts[0]]) {
				r.err = at(diagnostic("E-OPERATION", "operation requires imported alias and member"), e.tok)
			}
		}
	}
	if e.tag == "fn" {
		local := copyBound(bound)
		for _, p := range e.params {
			r.bind(local, p.name, p.tok)
		}
		r.block(e.body, local)
		return
	}
	if e.tag == "pipe" {
		r.expr(e.x, bound)
		local := copyBound(bound)
		local["$pipe"] = true
		r.expr(e.y.x, bound)
		for _, arg := range e.y.args {
			r.expr(arg.expr, local)
		}
		return
	}
	if e.tag == "comprehension" {
		r.expr(e.y, bound)
		local := copyBound(bound)
		r.bind(local, e.name, e.tok)
		r.expr(e.z, local)
		r.expr(e.x, local)
		return
	}
	if e.tag == "carry" {
		r.expr(e.x, bound)
		r.expr(e.y, bound)
		local := copyBound(bound)
		r.bind(local, e.state, e.tok)
		if e.name != "" {
			r.bind(local, e.name, e.tok)
		}
		r.block(e.body, local)
		return
	}
	for _, x := range []*expression{e.x, e.y, e.z} {
		r.expr(x, bound)
	}
	for _, arg := range e.args {
		r.expr(arg.expr, bound)
	}
	for _, x := range e.elems {
		r.expr(x, bound)
	}
	for _, field := range e.fields {
		r.expr(field.expr, bound)
	}
	if e.body != nil {
		r.block(e.body, copyBound(bound))
	}
	for _, arm := range e.arms {
		r.expr(arm.guard, bound)
		r.expr(arm.result, bound)
		if arm.body != nil {
			r.block(arm.body, copyBound(bound))
		}
	}
}
func (r *moduleRenamer) block(b *block, bound map[string]bool) {
	if b == nil {
		return
	}
	for _, s := range b.items {
		r.stmt(s, bound)
		if s.tag == "bind" || s.tag == "claim" {
			r.bind(bound, s.name, s.tok)
		}
	}
	r.expr(b.tail, bound)
	if b.exit != nil {
		r.stmt(b.exit, bound)
	}
}
func (r *moduleRenamer) stmt(s *statement, bound map[string]bool) {
	r.expr(s.expr, bound)
	if s.tag == "join" && !bound[s.name] {
		if name := r.names[s.name]; name != "" {
			s.name = name
		}
	}
	local := copyBound(bound)
	if s.tag == "for" && s.name != "" {
		r.bind(local, s.name, s.tok)
	}
	r.block(s.body, local)
	r.block(s.otherwise, copyBound(bound))
	if s.other != nil {
		r.stmt(s.other, copyBound(bound))
	}
	for _, a := range s.arms {
		r.expr(a.guard, bound)
		r.block(a.body, copyBound(bound))
	}
}
func mapSyntaxTokens(s *syntax, visit func(*token)) {
	seen := map[*expression]bool{}
	blocks := map[*block]bool{}
	var expr func(*expression)
	var blockWalk func(*block)
	var stmt func(*statement)
	expr = func(e *expression) {
		if e == nil || seen[e] {
			return
		}
		seen[e] = true
		visit(&e.tok)
		for i := range e.params {
			visit(&e.params[i].tok)
		}
		for i := range e.fields {
			visit(&e.fields[i].tok)
		}
		for i := range e.arms {
			visit(&e.arms[i].tok)
			for j := range e.arms[i].patterns {
				visit(&e.arms[i].patterns[j].tok)
			}
		}
		walkExpression(e, expr)
	}
	stmt = func(st *statement) {
		visit(&st.tok)
		expr(st.expr)
		blockWalk(st.body)
		blockWalk(st.otherwise)
		if st.other != nil {
			stmt(st.other)
		}
		for i := range st.arms {
			a := &st.arms[i]
			visit(&a.tok)
			for j := range a.patterns {
				visit(&a.patterns[j].tok)
			}
			expr(a.guard)
			expr(a.result)
			blockWalk(a.body)
		}
	}
	blockWalk = func(b *block) {
		if b == nil || blocks[b] {
			return
		}
		blocks[b] = true
		visit(&b.tok)
		for _, st := range b.items {
			stmt(st)
		}
		expr(b.tail)
		if b.exit != nil {
			stmt(b.exit)
		}
	}
	// walkExpression visits block expressions but not their statement tokens.
	// Follow block metadata explicitly too, with pointer sets preventing overlap.
	oldExpr := expr
	expr = func(e *expression) {
		if e == nil || seen[e] {
			return
		}
		oldExpr(e)
		blockWalk(e.body)
		for _, a := range e.arms {
			blockWalk(a.body)
		}
	}
	for i := range s.imports {
		visit(&s.imports[i].tok)
	}
	for _, f := range s.functions {
		expr(f)
	}
	for _, f := range s.flows {
		visit(&f.tok)
		for i := range f.params {
			visit(&f.params[i].tok)
		}
		blockWalk(f.body)
	}
	for name, d := range s.data {
		visit(&d.tok)
		s.data[name] = d
	}
	blockWalk(s.body)
}
