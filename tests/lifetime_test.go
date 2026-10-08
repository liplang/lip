package tests

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedDependencyLifetimes(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `import host "memory.*" as memory
flow Lifetimes(enabled: bool) -> object {
    raw = memory.allocate()
    mapped = [memory.read(raw, i) for i in range(3)]
    captured = list.map(mapped, fn(n) { n + len(mapped) })
    for i in range(3) {
        value = memory.read(raw, i)
        match i {
            1 => { continue },
            _ => { memory.record(value) }
        }
    }
    match enabled {
        true => { memory.read(raw, 10) },
        false => { memory.record(0) }
    }
    retried = retry(memory.read(raw, 9), 2)
    awaited = memory.await(raw)
    memory.check()
    return {mapped: mapped, captured: captured, retried: retried, awaited: awaited}
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"fmt";"reflect";goruntime "runtime";"sync";"testing";"weak";"lipalpha/runtime")
type payload [1<<20]byte
func newMemoryHost() runtime.Host {
    h:=runtime.NewHost()
    var mu sync.Mutex
    var probe weak.Pointer[payload]
    h.RegisterReadOnly("memory.allocate",func(context.Context,[]runtime.Value)runtime.Result{
        p:=new(payload);p[0]=7;mu.Lock();probe=weak.Make(p);mu.Unlock();return runtime.Ready(p)
    })
    h.RegisterReadOnly("memory.read",func(_ context.Context,a []runtime.Value)runtime.Result{
        return runtime.Ready(float64(a[0].(*payload)[0])+a[1].(float64))
    })
    h.Register("memory.record",func(context.Context,[]runtime.Value)runtime.Result{return runtime.Ready(nil)})
    h.Register("memory.await",func(_ context.Context,a []runtime.Value)runtime.Result{
        ch:=make(chan runtime.Result,1)
        go func(){ch<-runtime.Ready(float64(a[0].(*payload)[0]));close(ch)}()
        return runtime.Await(ch)
    })
    h.Register("memory.check",func(context.Context,[]runtime.Value)runtime.Result{
        goruntime.GC();mu.Lock();alive:=probe.Value()!=nil;mu.Unlock()
        if alive{return runtime.Failed(fmt.Errorf("large intermediate is still rooted"))}
        return runtime.Ready(nil)
    })
    return h
}
func TestLifetimes(t *testing.T){
    want:=map[string]runtime.Value{"mapped":[]runtime.Value{float64(7),float64(8),float64(9)},"captured":[]runtime.Value{float64(10),float64(11),float64(12)},"retried":float64(16),"awaited":float64(7)}
    for _,enabled:=range []bool{false,true}{
        inputs:=map[string]runtime.Value{"enabled":enabled}
        for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,4)}}{
            value,_,err:=run(context.Background(),newMemoryHost(),inputs)
            if err!=nil||!reflect.DeepEqual(value,want){t.Fatalf("enabled=%v: %v %v",enabled,value,err)}
        }
        instance,err:=NewInstance(newMemoryHost(),inputs);if err!=nil{t.Fatal(err)}
        for tick:=0;tick<3;tick++{
            value,_,err:=instance.Tick(context.Background(),nil)
            if err!=nil||!reflect.DeepEqual(value,want){t.Fatalf("tick %d: %v %v",tick,value,err)}
        }
    }
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated lifetimes: %v\n%s", err, out)
	}
}
