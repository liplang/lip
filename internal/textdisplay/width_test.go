package textdisplay

import "testing"

func TestDisplayClusters(t *testing.T) {
	for _, tc := range []struct {
		text            string
		width, clusters int
	}{
		{"abc", 3, 3}, {"你好", 4, 2}, {"e\u0301", 1, 1}, {"👩🏽‍💻", 2, 1}, {"🇨🇳", 2, 1}, {"✈️", 2, 1},
	} {
		runes, count := []rune(tc.text), 0
		for i := 0; i < len(runes); {
			next := Next(runes, i)
			if Previous(runes, next) != i {
				t.Fatalf("boundaries for %q", tc.text)
			}
			i = next
			count++
		}
		if Width(tc.text) != tc.width || count != tc.clusters {
			t.Fatalf("%q: width %d, clusters %d", tc.text, Width(tc.text), count)
		}
	}
	if got := ExpandTabs("你\tx"); got != "你  x" {
		t.Fatal(got)
	}
}
