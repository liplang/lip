package tests

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedHostGoAliases(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `import go "fmt" as f
import go "example.com/adapter" as a
import host "service.*" as s
import host "service.fetch" as fetch
import host "state" as load
import host "list.map" as custom
flow Aliases(input: number) -> object {
 message = f.Sprintf("value=%v", input)
 doubled = a.Double(input)
 fetched = retry(fetch(input), 2)
 verified = feedback(s.seed(input), s.step, s.verify, 3)
 loaded = load(input)
 host_map = custom(input)
 for i in range(2) {
  text = f.Sprintf("%v", input + i)
  s.emit(text)
 }
 return {message: message, doubled: doubled, fetched: fetched, verified: verified, loaded: loaded, host_map: host_map}
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"errors";"fmt";"reflect";"strings";"sync";"testing";"lipalpha/runtime")
func TestAliases(t *testing.T){
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,4)}}{
  host:=runtime.DefaultHost();var mu sync.Mutex;emitted:=[]runtime.Value{};attempts:=0
  host.RegisterPure("fmt.Sprintf",func(_ context.Context,args []runtime.Value)runtime.Result{return runtime.Ready(fmt.Sprintf(args[0].(string),args[1:]...))})
  host.RegisterPure("example.com/adapter.Double",func(_ context.Context,args []runtime.Value)runtime.Result{n,err:=runtime.Number(args[0]);if err!=nil{return runtime.Failed(err)};return runtime.Ready(n*2)})
  host.Register("service.fetch",func(_ context.Context,args []runtime.Value)runtime.Result{attempts++;if attempts==1{return runtime.Failed(errors.New("temporary"))};return runtime.Ready(args[0])})
  host.RegisterPure("service.seed",func(_ context.Context,args []runtime.Value)runtime.Result{return runtime.Ready(args[0])})
  host.RegisterPure("service.step",func(_ context.Context,args []runtime.Value)runtime.Result{n,_:=runtime.Number(args[0]);return runtime.Ready(n+1)})
  host.RegisterPure("service.verify",func(_ context.Context,args []runtime.Value)runtime.Result{n,_:=runtime.Number(args[0]);return runtime.Ready(n>=4)})
  host.Register("service.emit",func(_ context.Context,args []runtime.Value)runtime.Result{mu.Lock();emitted=append(emitted,args[0]);mu.Unlock();return runtime.Ready(nil)})
  host.Register("state",func(context.Context,[]runtime.Value)runtime.Result{return runtime.Ready("host-state")})
  host.Register("list.map",func(context.Context,[]runtime.Value)runtime.Result{return runtime.Ready("host-map")})
  value,_,err:=run(context.Background(),host,map[string]runtime.Value{"input":float64(2)})
  want:=map[string]runtime.Value{"message":"value=2","doubled":float64(4),"fetched":float64(2),"verified":float64(4),"loaded":"host-state","host_map":"host-map"}
  if err!=nil||!reflect.DeepEqual(value,want)||!reflect.DeepEqual(emitted,[]runtime.Value{"2","3"})||attempts!=2{t.Fatalf("value=%v emitted=%v attempts=%v err=%v",value,emitted,attempts,err)}
 }
 if _,_,err:=Run(context.Background(),runtime.DefaultHost(),map[string]runtime.Value{"input":float64(2)});err==nil||!strings.Contains(err.Error(),"fmt.Sprintf"){t.Fatalf("missing Go registration: %v",err)}
 deps:=RequiredDependencies();if deps[0].Kind!="go"||deps[0].Spec!="fmt"||deps[0].Alias!="f"||deps[2].Alias!="s"{t.Fatal(deps)}
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Host/Go aliases: %v\n%s", err, out)
	}
}
