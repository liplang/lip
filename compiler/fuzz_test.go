package compiler

import (
	"go/format"
	"testing"
)

func FuzzCompileSource(f *testing.F) {
	for _, seed := range []string{
		`flow Hello(x:string)->string{return string.trim(x)}`,
		`flow No()->number{return if true {1} else {fail("stop")}}`,
		`flow Choice(x:bool)->any{return match x {true=>1,false=>"off"}}`,
		`fn double(x:number?)->number? {match x {null=>null,_=>x*2}} print(double(null))`,
		`import host "fetch" as f; print(str(f()))`,
		`;;;import python "math" as print;;;print(print.sqrt(4));;;`,
		`import go "math" as twice; fn twice(x:number){;;x*2;;}; print(twice(2),twice.Abs(-1))`,
		`import host "emit" as string; string(1); print(string.trim(" x "))`,
		`import go "fmt" as f; for f in range(3) {match f {1=>{continue},_=>{print(f)}}}`,
		`match 1 {0 if false=>{print(0)},_=>{match true {true=>{print(1)},false=>{}}}}`,
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
