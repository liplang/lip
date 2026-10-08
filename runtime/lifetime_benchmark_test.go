package runtime

import (
	"context"
	"fmt"
	stdruntime "runtime"
	"testing"
)

// First-run cost includes graph construction and the lifetime plan. Generated
// one-shot Flow wrappers create a fresh graph; persistent Instances reuse it.
func BenchmarkValueLifetimeFirstRun(b *testing.B) {
	for _, release := range []bool{false, true} {
		b.Run(fmt.Sprintf("release=%v", release), func(b *testing.B) {
			inputs := map[string]Value{"input": 0}
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: release})
				previous := "input"
				for node := 0; node < 64; node++ {
					dep, name := previous, fmt.Sprint(node)
					g.Add(NodeSpec{Name: name, Deps: []string{dep}, Effect: EffectPure, Output: node == 63, Eval: func(_ context.Context, v map[string]Value) Result {
						return Ready(v[dep].(int) + 1)
					}})
					previous = name
				}
				if _, _, err := g.Run(context.Background(), inputs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkValueLifetime(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		for _, release := range []bool{false, true} {
			b.Run(fmt.Sprintf("parallel=%v/release=%v", parallel, release), func(b *testing.B) {
				g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: release})
				previous := "input"
				for n := 0; n < 64; n++ {
					dep := previous
					name := fmt.Sprint(n)
					g.Add(NodeSpec{Name: name, Deps: []string{dep}, Effect: EffectPure, Output: n == 63, Eval: func(_ context.Context, v map[string]Value) Result {
						return Ready(v[dep].(int) + 1)
					}})
					previous = name
				}
				inputs := map[string]Value{"input": 0}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					var err error
					if parallel {
						_, _, err = g.RunParallel(context.Background(), NewHost(), inputs, 4)
					} else {
						_, _, err = g.Run(context.Background(), inputs)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Compare live heap at the same point *during* execution, after 16 independent
// 1 MiB buffers have been consumed. Allocated bytes remain essentially equal;
// the benefit is making the buffers eligible for collection sooner.
func BenchmarkIntermediateRetention(b *testing.B) {
	for _, release := range []bool{false, true} {
		b.Run(fmt.Sprintf("release=%v", release), func(b *testing.B) {
			var baseline, live uint64
			g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: release})
			g.Add(NodeSpec{Name: "baseline", Eval: func(context.Context, map[string]Value) Result {
				stdruntime.GC()
				var stats stdruntime.MemStats
				stdruntime.ReadMemStats(&stats)
				baseline = stats.HeapAlloc
				return Ready(nil)
			}})
			for n := 0; n < 16; n++ {
				name := fmt.Sprint(n)
				g.Add(NodeSpec{Name: name, Eval: func(context.Context, map[string]Value) Result {
					return Ready(make([]byte, 1<<20))
				}})
				g.Add(NodeSpec{Name: "consume" + name, Deps: []string{name}, Eval: func(_ context.Context, v map[string]Value) Result {
					return Ready(len(v[name].([]byte)))
				}})
			}
			g.Add(NodeSpec{Name: "observe", Eval: func(context.Context, map[string]Value) Result {
				stdruntime.GC()
				var stats stdruntime.MemStats
				stdruntime.ReadMemStats(&stats)
				if stats.HeapAlloc > baseline {
					live = stats.HeapAlloc - baseline
				} else {
					live = 0
				}
				return Ready(nil)
			}})
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if _, _, err := g.Run(context.Background(), nil); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(live), "live-B")
		})
	}
}
