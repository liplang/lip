package runtime

// A plan has names only once; per-execution counters and consumer lists use
// integer slots. It contains no values and can be reused across runs and ticks.
type valueLifetimePlan struct {
	names     []string
	byName    map[string]int
	counts    []int
	consumers [][]int
	results   []int
}

// valueLifetime tracks consumers, not ownership of the underlying objects.
// Only the scheduler touches these counts and the execution values table.
// Running workers keep their own dependency snapshots until Await completes.
type valueLifetime struct {
	plan      *valueLifetimePlan
	remaining []int
}

func buildValueLifetimePlan(nodes []NodeSpec) *valueLifetimePlan {
	plan := &valueLifetimePlan{byName: make(map[string]int), consumers: make([][]int, len(nodes)), results: make([]int, len(nodes))}
	for index, node := range nodes {
		for _, refs := range [2][]string{node.Deps, node.Gates} {
			for _, name := range refs {
				slot, ok := plan.byName[name]
				if !ok {
					slot = len(plan.names)
					plan.byName[name] = slot
					plan.names = append(plan.names, name)
					plan.counts = append(plan.counts, 0)
				}
				plan.counts[slot]++
				plan.consumers[index] = append(plan.consumers[index], slot)
			}
		}
	}
	for index, node := range nodes {
		plan.results[index] = -1
		if slot, ok := plan.byName[node.Name]; ok {
			plan.results[index] = slot
		}
	}
	return plan
}

func (g *Graph) newValueLifetime(values map[string]Value) *valueLifetime {
	if !g.options.ReleaseIntermediates {
		return nil
	}
	if g.lifetimePlan == nil {
		g.lifetimePlan = buildValueLifetimePlan(g.nodes)
	}
	lifetime := &valueLifetime{plan: g.lifetimePlan, remaining: append([]int(nil), g.lifetimePlan.counts...)}
	for name := range values {
		if _, used := g.lifetimePlan.byName[name]; !used {
			delete(values, name)
		}
	}
	return lifetime
}

// finish must run exactly once for each completed or skipped consumer, after
// saving any output and persistent cache. After edges consume status only.
func (lifetime *valueLifetime) finish(index int, node NodeSpec, values map[string]Value) {
	if lifetime == nil {
		return
	}
	for _, slot := range lifetime.plan.consumers[index] {
		lifetime.remaining[slot]--
		if lifetime.remaining[slot] == 0 {
			delete(values, lifetime.plan.names[slot])
		}
	}
	slot := lifetime.plan.results[index]
	if slot < 0 || lifetime.remaining[slot] == 0 {
		delete(values, node.Name)
	}
}

// nodeValues avoids carrying unrelated objects into long-running workers.
// A missing dependency stays absent: a present nil is a legitimate LIP null.
func nodeValues(node NodeSpec, values map[string]Value, release bool) map[string]Value {
	if !release {
		return cloneValues(values)
	}
	snapshot := make(map[string]Value, len(node.Deps)+len(node.Gates))
	for _, refs := range [2][]string{node.Deps, node.Gates} {
		for _, name := range refs {
			if value, present := values[name]; present {
				snapshot[name] = value
			}
		}
	}
	return snapshot
}
