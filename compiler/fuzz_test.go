package compiler

import (
	"go/format"
	"testing"
)

func FuzzCompileSource(f *testing.F) {
	for _, seed := range []string{
		`flow Hello(x:string)->string{return string.trim(x)}`,
		`flow No()->number{return if true {1} else {fail("stop")}}`,
		`fn f(x:number)->number{return x+1} flow Map()->list{return list.map(range(0,3),f)}`,
		`fn f(n:number)->list{return [[x+y for y in range(0,x)] for x in range(1,n)]} print(f(4))`,
		`factor=3; print(list.map(range(4),fn(x){list.map(range(x),fn(y){x+y*factor})}))`,
		`print(fold(list.filter(range(5),fn(x){x>1}),0,fn(total,x){total+x}))`,
		`flow 中文(值:object)->any{return {数据:值.name}}`,
		`flow Bad(`, `flow Bad()->number{return 1e}`, `"`, `!`, "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 4096 {
			t.Skip()
		}
		graph, err := ParseAndBuild(source)
		if err != nil {
			return
		}
		code, err := GenerateGo(graph)
		if err != nil {
			t.Fatalf("accepted source cannot generate: %v\n%s", err, source)
		}
		if _, err := format.Source([]byte(code)); err != nil {
			t.Fatalf("accepted source generates invalid Go: %v\n%s", err, source)
		}
	})
}
