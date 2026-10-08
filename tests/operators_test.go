package tests

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedNumericOperators(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `# Operators compose with fn, match and callbacks.
fn square(x: number) -> number { x ** 2 }
flow Arithmetic(base: any, exponent: any, mode: number) -> object {
 return {
  floor: 7 // 2, negative_floor: -7 // 2, negative_divisor: 7 // -2,
  left: 20 // 3 // 2, precedence: 1 + 2 ** 3 * 4,
  right: 2 ** 3 ** 2, negative: -2 ** 2, grouped: (-2) ** 2,
  negative_power: 2 ** -2, nested_negative_power: 2 ** -2 ** 2,
  composed: list.map(range(4), fn(x) { square(x) // 2 }),
  dynamic: base ** exponent,
  modulo: -7 % 2, negative_modulo: 7 % -2, logarithm: 8 */ 2,
  log_precedence: 16 */ 2 ** 2, log_left: 8 */ 2 */ 3,
  lazy: match mode { 0 => 1, 1 => 1 // 0, 2 => 2 ** 1024, 3 => (-2) ** 0.5, 4 => 1 % 0, 5 => 0 */ 2, _ => 8 */ 1 }
 }
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"reflect";"strings";"testing";"lipalpha/runtime")
func TestOperators(t *testing.T) {
 want:=map[string]runtime.Value{"floor":float64(3),"negative_floor":float64(-4),"negative_divisor":float64(-4),"left":float64(3),"precedence":float64(33),"right":float64(512),"negative":float64(-4),"grouped":float64(4),"negative_power":0.25,"nested_negative_power":0.0625,"composed":[]runtime.Value{float64(0),float64(0),float64(2),float64(4)},"dynamic":float64(8),"lazy":float64(1),"modulo":float64(1),"negative_modulo":float64(-1),"logarithm":float64(3),"log_precedence":float64(2),"log_left":float64(1)}
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,3)}} {
  value,_,err:=run(context.Background(),runtime.DefaultHost(),map[string]runtime.Value{"base":2,"exponent":3,"mode":0})
  if err!=nil||!reflect.DeepEqual(value,want){t.Fatalf("value=%v err=%v",value,err)}
  for _,tc:=range []struct{base,exponent runtime.Value;mode int;want string}{{2,3,1,"division by zero"},{2,3,2,"not finite"},{2,3,3,"not finite"},{2,3,4,"modulo by zero"},{2,3,5,"argument must be positive"},{2,3,6,"base must be positive"},{"2",3,0,"expected number"},{2,true,0,"expected number"}}{
   _,_,err:=run(context.Background(),runtime.DefaultHost(),map[string]runtime.Value{"base":tc.base,"exponent":tc.exponent,"mode":tc.mode})
   if err==nil||!strings.Contains(err.Error(),tc.want){t.Fatalf("%+v: %v",tc,err)}
  }
 }
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("numeric operators: %v\n%s", err, out)
	}
}
