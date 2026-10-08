package tests

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedForExecution(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `import host "service.*"
fn double(x: number) -> number { x * 2 }
flow Each(values: any, scale: number, enabled: bool) -> number {
 service.emit("before")
 match enabled {
  true => {
   local = scale
   for i in service.source(values) {
    match i {
     1 => { continue },
     4 => { break },
     _ => {}
    }
    doubled = double(i) * local
    service.emit(doubled)
    for j in range(4) {
     match j {
      0 => { continue },
      2 => { break },
      _ => { service.emit(i * 10 + j) }
     }
    }
    service.emit(i + 100)
   }
  },
  false => { local = 99; for i in [] { service.emit(local) } }
 }
 for i in [8] { for i in range(i - 6) { service.emit(i + 200) }; service.emit(i + 300) }
 service.emit("after")
 return scale
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"errors";"reflect";"strings";"sync";"testing";"lipalpha/runtime")
type recorder struct { mu sync.Mutex; emitted []runtime.Value; sourceCalls int; failAt runtime.Value; cancel context.CancelFunc }
func (r *recorder) host(effect runtime.Effect) runtime.Host {
 h:=runtime.DefaultHost()
 emit:=func(_ context.Context,args []runtime.Value)runtime.Result{
  r.mu.Lock();defer r.mu.Unlock()
  r.emitted=append(r.emitted,args[0])
  if reflect.DeepEqual(args[0],r.failAt){return runtime.Failed(errors.New("stop here"))}
  if r.cancel!=nil && reflect.DeepEqual(args[0],float64(12)){r.cancel()}
  future:=make(chan runtime.Result,1);future<-runtime.Ready(nil);return runtime.Result{Future:future}
 }
 register:=func(name string,op runtime.Op){switch effect{case runtime.EffectPure:h.RegisterPure(name,op);case runtime.EffectReadOnly:h.RegisterReadOnly(name,op);default:h.Register(name,op)}}
 register("service.emit",emit)
 register("service.source",func(_ context.Context,args []runtime.Value)runtime.Result{
  r.mu.Lock();r.sourceCalls++;r.mu.Unlock()
  future:=make(chan runtime.Result,1);future<-runtime.Ready(args[0]);return runtime.Result{Future:future}
 })
 return h
}
func inputs(values runtime.Value,scale int,enabled bool)map[string]runtime.Value{return map[string]runtime.Value{"values":values,"scale":scale,"enabled":enabled}}
func expected(scale int)[]runtime.Value{
 return []runtime.Value{"before",float64(0),float64(1),float64(100),float64(4*scale),float64(21),float64(102),float64(6*scale),float64(31),float64(103),float64(200),float64(201),float64(308),"after"}
}
func TestFor(t *testing.T) {
 for _,effect:=range []runtime.Effect{runtime.EffectPure,runtime.EffectReadOnly,runtime.EffectExternalWrite}{
  for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,4)}}{
   r:=&recorder{};value,trace,err:=run(context.Background(),r.host(effect),inputs([]int{0,1,2,3,4,5},3,true))
   if err!=nil||value!=3||!reflect.DeepEqual(r.emitted,expected(3))||r.sourceCalls!=1{t.Fatalf("effect=%v value=%v emitted=%v sources=%v trace=%v err=%v",effect,value,r.emitted,r.sourceCalls,trace,err)}
   for _,event:=range trace{if event.Status==runtime.Error||event.Status==runtime.Cancelled{t.Fatalf("control leaked: %v",trace)}}
   for _,tc:=range []struct{values runtime.Value;enabled bool;want []runtime.Value;sources int}{
    {[]int{},true,[]runtime.Value{"before",float64(200),float64(201),float64(308),"after"},1},
    {[]int{1,4,99},true,[]runtime.Value{"before",float64(200),float64(201),float64(308),"after"},1},
    {[]int{0},false,[]runtime.Value{"before",float64(200),float64(201),float64(308),"after"},0},
   }{r=&recorder{};_,_,err=run(context.Background(),r.host(effect),inputs(tc.values,3,tc.enabled));if err!=nil||!reflect.DeepEqual(r.emitted,tc.want)||r.sourceCalls!=tc.sources{t.Fatalf("case=%+v emitted=%v sources=%d err=%v",tc,r.emitted,r.sourceCalls,err)}}
   r=&recorder{failAt:float64(12)};_,_,err=run(context.Background(),r.host(effect),inputs([]int{0,1,2,3},3,true))
   if err==nil||!strings.Contains(err.Error(),"for iteration 2")||!strings.Contains(err.Error(),"stop here")||!reflect.DeepEqual(r.emitted,[]runtime.Value{"before",float64(0),float64(1),float64(100),float64(12)}){t.Fatalf("failed iteration: %v, %v",r.emitted,err)}
   ctx,cancel:=context.WithCancel(context.Background());r=&recorder{cancel:cancel};_,_,err=run(ctx,r.host(effect),inputs([]int{0,1,2,3},3,true));cancel()
   if !errors.Is(err,context.Canceled)||!reflect.DeepEqual(r.emitted,[]runtime.Value{"before",float64(0),float64(1),float64(100),float64(12)}){t.Fatalf("cancel: %v, %v",r.emitted,err)}
   r=&recorder{};_,_,err=run(context.Background(),r.host(effect),inputs("bad",3,true));if err==nil||!strings.Contains(err.Error(),"for source")||!reflect.DeepEqual(r.emitted,[]runtime.Value{"before"}){t.Fatalf("source: %v, %v",r.emitted,err)}
  }
  r:=&recorder{};instance,err:=NewInstance(r.host(effect),inputs([]int{0,1,2,3,4,5},3,true));if err!=nil{t.Fatal(err)}
  for _,scale:=range []int{3,3,4,4}{
   start:=len(r.emitted);_,_,err:=instance.Tick(context.Background(),map[string]runtime.Value{"scale":scale})
   if err!=nil{t.Fatal(err)}
   // The enclosing Host's pure/read-only calls may be cached; loop body calls always rerun.
   emitted:=r.emitted[start:];if len(emitted)>0&&emitted[0]=="before"{emitted=emitted[1:]};if len(emitted)>0&&emitted[len(emitted)-1]=="after"{emitted=emitted[:len(emitted)-1]}
   want:=expected(scale);want=want[1:len(want)-1];if !reflect.DeepEqual(emitted,want){t.Fatalf("tick scale=%d emitted=%v want=%v",scale,emitted,want)}
  }
  if r.sourceCalls!=4{t.Fatal(r.sourceCalls)}
 }
 h:=runtime.DefaultHost();h.Register("service.unused",func(context.Context,[]runtime.Value)runtime.Result{return runtime.Ready(nil)})
 if _,_,err:=Run(context.Background(),h,inputs([]int{},3,false));err==nil||!strings.Contains(err.Error(),"service.emit"){t.Fatalf("missing loop Host operation: %v",err)}
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("for execution: %v\n%s", err, out)
	}
}
