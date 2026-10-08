package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedAuditExecution(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `import host "s.*" as s
import host "double" as remote
import python "math" as p
import go "math" as g
fn double(x: number?) -> number? { match x { null => null, _ => x * 2 } }
fn local(p: object) -> any { p.pi }
flow Audit(x: number?, flag: bool) -> object {
 initial = state(double(s.initial()))
 s.mark(-1)
 nested = double(s.mark(1))
 mapped = [double(s.mark(i)) for i in s.items()]
 composed = len([s.mark(i) for i in range(4,6)])
 paired = s.pair(s.mark(6), s.mark(7))
 lazy = match flag { true => s.mark(8), false => 0 }
 retried = retry(double(s.mark(9)), 2)
 candidate = feedback(s.seed(), s.step, s.verify, 3)
	 cached = double(s.pure(2))
 read = double(s.read(2))
 remote_value = remote(3)
 remote_pair = double(remote(4))
 conditional = if flag { 1 } else { "disabled" }
 match flag {
  true => { p = {pi: 3}; s.local(p.pi) },
  false => { s.local(p.pi) }
 }
 pi = p.pi
 s.mark(10)
 return {nested:nested, mapped:mapped, composed:composed, paired:paired, lazy:lazy,
         retried:retried, candidate:candidate, cached:cached, input:double(x),
         pi:pi, local:local({pi:3}), remote:remote_value, remote_pair:remote_pair, read:read, conditional:conditional, initial:initial}
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"errors";"fmt";"reflect";"strings";"sync";"testing";"lipalpha/runtime")
type recorder struct{mu sync.Mutex;logs []string;pure int;reads int;fail bool}
func(r *recorder)log(v string){r.mu.Lock();defer r.mu.Unlock();r.logs=append(r.logs,v)}
func(r *recorder)host(pure bool)runtime.Host{
 h:=runtime.DefaultHost()
 mark:=func(_ context.Context,a []runtime.Value)runtime.Result{
  n,_:=runtime.Number(a[0]);r.log(fmt.Sprint(n));if r.fail&&n==6{return runtime.Failed(errors.New("stop at six"))}
  ch:=make(chan runtime.Result,1);go func(){ch<-runtime.Ready(a[0])}();return runtime.Await(ch)
 }
 if pure{h.RegisterPure("s.mark",mark)}else{h.Register("s.mark",mark)}
 h.Register("s.initial",func(context.Context,[]runtime.Value)runtime.Result{r.log("initial");return runtime.Ready(3)})
 h.Register("s.items",func(context.Context,[]runtime.Value)runtime.Result{r.log("items");return runtime.Ready([]int{2,3})})
 h.Register("s.pair",func(_ context.Context,a []runtime.Value)runtime.Result{r.log("pair");return runtime.Ready(a)})
 h.Register("s.seed",func(context.Context,[]runtime.Value)runtime.Result{r.log("seed");return runtime.Ready(0)})
 h.Register("s.step",func(_ context.Context,a []runtime.Value)runtime.Result{r.log("step");n,_:=runtime.Number(a[0]);return runtime.Ready(n+1)})
 h.RegisterPure("s.verify",func(_ context.Context,a []runtime.Value)runtime.Result{n,_:=runtime.Number(a[0]);return runtime.Ready(n>=2)})
 h.Register("double",func(_ context.Context,a []runtime.Value)runtime.Result{r.log("remote");n,_:=runtime.Number(a[0]);return runtime.Ready(n+100)})
 h.RegisterReadOnly("s.read",func(_ context.Context,a []runtime.Value)runtime.Result{r.mu.Lock();r.reads++;r.mu.Unlock();return runtime.Ready(a[0])})
 h.RegisterPure("s.pure",func(_ context.Context,a []runtime.Value)runtime.Result{r.mu.Lock();r.pure++;r.mu.Unlock();return runtime.Ready(a[0])})
 h.Register("s.local",func(context.Context,[]runtime.Value)runtime.Result{return runtime.Ready(nil)})
 h.RegisterPure("python.getattr",func(context.Context,[]runtime.Value)runtime.Result{return runtime.Ready(3.14)})
 return h
}
func TestAudit(t *testing.T){
 wantLogs:=[]string{"initial","-1","1","items","2","3","4","5","6","7","pair","8","9","seed","step","step","remote","remote","10"}
 want:=map[string]runtime.Value{"nested":float64(2),"mapped":[]runtime.Value{float64(4),float64(6)},"composed":float64(2),"paired":[]runtime.Value{float64(6),float64(7)},"lazy":float64(8),"retried":float64(18),"candidate":float64(2),"cached":float64(4),"input":nil,"pi":3.14,"local":float64(3),"remote":float64(103),"remote_pair":float64(208),"read":float64(4),"conditional":float64(1),"initial":float64(6)}
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,4)}}{
  r:=&recorder{};value,_,err:=run(context.Background(),r.host(false),map[string]runtime.Value{"x":nil,"flag":true})
  if err!=nil||!reflect.DeepEqual(value,want)||!reflect.DeepEqual(r.logs,wantLogs){t.Fatalf("value=%v logs=%v err=%v",value,r.logs,err)}
  r=&recorder{fail:true};_,_,err=run(context.Background(),r.host(false),map[string]runtime.Value{"x":nil,"flag":true})
  if err==nil||!strings.Contains(err.Error(),"stop at six")||!reflect.DeepEqual(r.logs,wantLogs[:9]){t.Fatalf("error order: %v %v",r.logs,err)}
  r=&recorder{};value,_,err=run(context.Background(),r.host(false),map[string]runtime.Value{"x":float64(3),"flag":false})
  if err!=nil||value.(map[string]runtime.Value)["input"]!=float64(6)||value.(map[string]runtime.Value)["lazy"]!=float64(0)||value.(map[string]runtime.Value)["conditional"]!="disabled"{t.Fatalf("optional/lazy: %v %v",value,err)}
  for _,log:=range r.logs{if log=="8"{t.Fatal("unchosen match arm executed")}}
  ctx,cancel:=context.WithCancel(context.Background());cancel();_,_,err=run(ctx,r.host(false),map[string]runtime.Value{"x":nil,"flag":true});if !errors.Is(err,context.Canceled){t.Fatal(err)}
 }
 for _,pure:=range []bool{false,true}{
  r:=&recorder{};instance,err:=NewInstance(r.host(pure),map[string]runtime.Value{"x":nil,"flag":true});if err!=nil{t.Fatal(err)}
  for tick:=0;tick<3;tick++{_,_,err=instance.Tick(context.Background(),nil);if err!=nil{t.Fatal(err)}}
  counts:=map[string]int{};for _,log:=range r.logs{counts[log]++}
  if counts["items"]!=3||counts["step"]!=6||counts["pair"]!=3||r.pure!=1||r.reads!=3||counts["remote"]!=6||counts["initial"]!=1{t.Fatalf("effect/cache: %v pure=%v",counts,r.pure)}
  wantNested:=3;if pure{wantNested=1};if counts["1"]!=wantNested||counts["9"]!=wantNested{t.Fatalf("nested write/retry caching: %v",counts)}
 }
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("audit execution: %v\n%s", err, out)
	}
}

func TestGeneratedFeedbackCoreOperations(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `fn done(x: string) -> bool { x == "YES" }
flow Feedback() -> string { return feedback(string.trim(" yes "), string.upper, done, 2) }`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"testing";"lipalpha/runtime")
func TestCore(t *testing.T){value,_,err:=Run(context.Background(),runtime.DefaultHost(),nil);if err!=nil||value!="YES"{t.Fatalf("%v %v",value,err)}}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("core feedback: %v\n%s", err, out)
	}
}

func TestGeneratedNestedLoopErrorPosition(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	path := filepath.Join(dir, "flow.go")
	writeTestFile(t, path, generatedSource(t, "for i in range(1) {\n for j in range(1) {\n  print(1 / 0)\n }\n}", true))
	binary := filepath.Join(dir, "flow")
	build := exec.Command("go", "build", "-o", binary, path)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := exec.Command(binary).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "<input>:3:3:") || !strings.Contains(string(out), "print(1 / 0)") {
		t.Fatalf("loop error position: %v\n%s", err, out)
	}
}
