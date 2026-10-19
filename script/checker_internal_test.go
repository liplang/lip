package script

import "testing"

func TestStaticDescriptionStructuralIdentity(t *testing.T) {
	unknownList := listDesc(nil, nil)
	emptyList := listDesc([]*staticDesc{}, nil)
	if descOrderKey(unknownList) == descOrderKey(emptyList) {
		t.Fatal("unknown and exact-empty lists share an order key")
	}
	closed := objectDesc(map[string]*staticDesc{"a": plainDesc("number")}, nil, true)
	open := objectDesc(map[string]*staticDesc{"a": plainDesc("number")}, nil, false)
	if descOrderKey(closed) == descOrderKey(open) {
		t.Fatal("open and closed objects share an order key")
	}
	left := requestDesc(paramOperations(0))
	right := requestDesc(paramOperations(1))
	if sameDesc(left, right) || descOrderKey(left) == descOrderKey(right) {
		t.Fatal("different parameter operation summaries were collapsed")
	}

	mapped0 := requestDesc(&operationSummary{mapped: &operationSummary{}, args: []*staticDesc{plainDesc("number")}})
	mapped1 := requestDesc(&operationSummary{mapped: &operationSummary{}, args: []*staticDesc{plainDesc("string")}})
	if sameDesc(mapped0, mapped1) || descOrderKey(mapped0) == descOrderKey(mapped1) {
		t.Fatal("mapped operation summaries ignored their abstract arguments")
	}
	ordered := requestDesc(unionOperations(paramOperations(0), paramOperations(1)))
	reversed := requestDesc(unionOperations(paramOperations(1), paramOperations(0)))
	if !sameDesc(ordered, reversed) {
		t.Fatal("operation-summary union order changed descriptor identity")
	}
}

func TestKeysDescUnionShape(t *testing.T) {
	d := unionDesc(
		objectDesc(map[string]*staticDesc{"a": plainDesc("number")}, nil, true),
		objectDesc(map[string]*staticDesc{"b": plainDesc("number")}, nil, true),
	)
	keys := keysDesc(d)
	slots, tail, ok := listShape(keys)
	if !ok || tail || len(slots) != 1 || slots[0] == nil || descType(slots[0]) != "string" {
		t.Fatalf("keys union lost exact shape: desc=%#v slots=%d tail=%v ok=%v", keys, len(slots), tail, ok)
	}
}
