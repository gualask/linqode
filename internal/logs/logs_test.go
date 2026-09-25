package logs

import (
	"slices"
	"testing"
)

func TestSplitsLinesAcrossChunkBoundaries(t *testing.T) {
	var a LineAssembler
	if got := a.Push([]byte("first li")); got != nil {
		t.Errorf("got %q", got)
	}
	if got := a.Push([]byte("ne\nsecond\nthi")); !slices.Equal(got, []string{"first line", "second"}) {
		t.Errorf("got %q", got)
	}
	if got := a.Push([]byte("rd\n")); !slices.Equal(got, []string{"third"}) {
		t.Errorf("got %q", got)
	}
	if line, ok := a.Finish(); ok {
		t.Errorf("unexpected trailing line %q", line)
	}
}

func TestStripsCRLFAndFlushesTrailingPartial(t *testing.T) {
	var a LineAssembler
	if got := a.Push([]byte("windows\r\ntail")); !slices.Equal(got, []string{"windows"}) {
		t.Errorf("got %q", got)
	}
	if line, ok := a.Finish(); !ok || line != "tail" {
		t.Errorf("got %q ok=%v", line, ok)
	}
}

func TestReplacesInvalidUTF8(t *testing.T) {
	var a LineAssembler
	if got := a.Push([]byte("a\xff b\n")); !slices.Equal(got, []string{"a� b"}) {
		t.Errorf("got %q", got)
	}
}

func TestUTF8SplitAcrossChunksSurvives(t *testing.T) {
	var a LineAssembler
	bytes := []byte("è\n") // 0xC3 0xA8 0x0A
	if got := a.Push(bytes[:1]); got != nil {
		t.Errorf("got %q", got)
	}
	if got := a.Push(bytes[1:]); !slices.Equal(got, []string{"è"}) {
		t.Errorf("got %q", got)
	}
}

func TestTailDropsOldestWhenFull(t *testing.T) {
	b := NewTailBuffer[string](2)
	if _, dropped := b.Push("a"); dropped {
		t.Error("a dropped")
	}
	if _, dropped := b.Push("b"); dropped {
		t.Error("b dropped")
	}
	oldest, dropped := b.Push("c")
	if !dropped || oldest != "a" {
		t.Errorf("got %q dropped=%v", oldest, dropped)
	}
	var items []string
	for i := range b.Len() {
		item, _ := b.Get(i)
		items = append(items, item)
	}
	if !slices.Equal(items, []string{"b", "c"}) {
		t.Errorf("got %q", items)
	}
}

func TestTailGetIndexesFromOldest(t *testing.T) {
	b := NewTailBuffer[int](10)
	b.Push(1)
	b.Push(2)
	b.Push(3)
	if v, ok := b.Get(1); !ok || v != 2 {
		t.Errorf("Get(1) = %d ok=%v", v, ok)
	}
	if _, ok := b.Get(3); ok {
		t.Error("Get(3) should be out of range")
	}
}

func TestTailKeepsWrappingAfterManyDrops(t *testing.T) {
	b := NewTailBuffer[int](3)
	for i := range 10 {
		b.Push(i)
	}
	var items []int
	for i := range b.Len() {
		item, _ := b.Get(i)
		items = append(items, item)
	}
	if !slices.Equal(items, []int{7, 8, 9}) {
		t.Errorf("got %v", items)
	}
}

func TestFindASCIICI(t *testing.T) {
	if got := FindASCIICI("Hello World", "WORLD"); got != 6 {
		t.Errorf("got %d", got)
	}
	if got := FindASCIICI("Hello", "xyz"); got != -1 {
		t.Errorf("got %d", got)
	}
	// Multi-byte UTF-8 never matches folded ASCII, so offsets stay on rune
	// boundaries.
	if got := FindASCIICI("caffè LATTE", "latte"); got != 7 {
		t.Errorf("got %d", got)
	}
}

func TestStoreSearchWrapsBothWays(t *testing.T) {
	s := NewStore(100)
	for _, line := range []string{"alpha one", "beta", "ALPHA two"} {
		s.Push(line)
	}
	if i, ok := s.SearchNext("alpha", 0, nil); !ok || i != 0 {
		t.Errorf("next from 0: %d ok=%v", i, ok)
	}
	if i, ok := s.SearchNext("alpha", 1, nil); !ok || i != 2 {
		t.Errorf("next from 1: %d ok=%v", i, ok)
	}
	if i, ok := s.SearchNext("beta", 2, nil); !ok || i != 1 { // wrapped
		t.Errorf("next from 2: %d ok=%v", i, ok)
	}
	if i, ok := s.SearchPrev("alpha", 1, nil); !ok || i != 0 {
		t.Errorf("prev from 1: %d ok=%v", i, ok)
	}
	if i, ok := s.SearchPrev("two", 0, nil); !ok || i != 2 { // wrapped
		t.Errorf("prev from 0: %d ok=%v", i, ok)
	}
	if _, ok := s.SearchNext("missing", 0, nil); ok {
		t.Error("found a line that does not exist")
	}
}

func TestStoreReportsDropsForScrollCompensation(t *testing.T) {
	s := NewStore(2)
	if got := s.Push("a"); got != 0 {
		t.Errorf("got %d", got)
	}
	if got := s.Push("b"); got != 0 {
		t.Errorf("got %d", got)
	}
	if got := s.Push("c"); got != 1 {
		t.Errorf("got %d", got)
	}
	if line, _ := s.Line(0); line.Raw != "b" {
		t.Errorf("oldest is %q", line.Raw)
	}
}
