package memscan

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/kayon/memscan/scanner"
)

func statusOf(s *Session, typ scanner.Type) (TypeStatus, bool) {
	for _, info := range s.Infos() {
		if info.Type == typ {
			return info.Status, true
		}
	}
	return 0, false
}

func requireStatus(t *testing.T, s *Session, typ scanner.Type, want TypeStatus) {
	t.Helper()
	got, ok := statusOf(s, typ)
	if !ok {
		t.Fatalf("%v: not part of the session", typ)
	}
	if got != want {
		t.Fatalf("%v: status %v, want %v", typ, got, want)
	}
}

func (a *plantedArea) hits(s *Session, typ scanner.Type) []uint64 {
	m := s.Scan(typ)
	if m == nil {
		return nil
	}
	return a.within(m.Results())
}

func mustFirstScan(t *testing.T, s *Session, values ...*scanner.Value) {
	t.Helper()
	if _, err := s.FirstScan(values, false); err != nil {
		t.Fatal(err)
	}
}

func mustNextScan(t *testing.T, s *Session, values ...*scanner.Value) {
	t.Helper()
	if _, err := s.NextScan(values); err != nil {
		t.Fatal(err)
	}
}

func TestSessionNextScanRefinesEachType(t *testing.T) {
	area := newPlantedArea(t, 100*4096)
	x, x2 := scanner.NewInt32(0x6F1E9A3C), scanner.NewInt32(0x71AB2C05)
	b, b2 := scanner.NewInt64(0x1D2C3B4A5F607182), scanner.NewInt64(0x0E1F2A3B4C5D6E7F)

	xAddrs := area.plant(x, slots(60, 0)...)
	bAddrs := area.plant(b, slots(60, 1)...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, x, b)
	if s.Rounds() != 1 || s.CanUndo() {
		t.Fatalf("after the first scan: round %d, canUndo %v", s.Rounds(), s.CanUndo())
	}

	// half of the Int32 values change, all the Int64 values change
	area.plant(x2, slots(30, 0)...)
	area.plant(b2, slots(60, 1)...)

	mustNextScan(t, s, x2, b2)
	if s.Rounds() != 2 || !s.CanUndo() {
		t.Fatalf("after the next scan: round %d, canUndo %v", s.Rounds(), s.CanUndo())
	}
	requireStatus(t, s, scanner.Int32, StatusOK)
	requireStatus(t, s, scanner.Int64, StatusOK)

	if got := area.hits(s, scanner.Int32); !reflect.DeepEqual(got, xAddrs[:30]) {
		t.Fatalf("Int32: %d hits, want the %d that changed", len(got), 30)
	}
	if got := area.hits(s, scanner.Int64); !reflect.DeepEqual(got, bAddrs) {
		t.Fatalf("Int64: %d hits, want %d", len(got), len(bAddrs))
	}
	assertSorted(t, "Int32", s.Scan(scanner.Int32).Results())

	if !s.UndoScan() {
		t.Fatal("nothing to undo")
	}
	if s.Rounds() != 1 || s.CanUndo() {
		t.Fatalf("after undo: round %d, canUndo %v", s.Rounds(), s.CanUndo())
	}
	if got := area.hits(s, scanner.Int32); !reflect.DeepEqual(got, xAddrs) {
		t.Fatalf("Int32 after undo: %d hits, want %d", len(got), len(xAddrs))
	}
	if s.UndoScan() {
		t.Fatal("undo must be possible only once")
	}
}

func TestSessionNextScanDropsTypesWithoutValue(t *testing.T) {
	area := newPlantedArea(t, 100*4096)
	small := scanner.NewInt8(0x5A)
	x := scanner.NewInt32(0x6F1E9A3C)
	b := scanner.NewInt64(0x1D2C3B4A5F607182)
	area.plant(small, slots(40, 2)...)
	area.plant(x, slots(40, 0)...)
	bAddrs := area.plant(b, slots(40, 1)...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, small, x, b)
	smallBefore := s.Scan(scanner.Int8).Count()

	// only the Int32 expression of the new value exists
	mustNextScan(t, s, x)
	requireStatus(t, s, scanner.Int32, StatusOK)
	requireStatus(t, s, scanner.Int8, StatusDropped)
	requireStatus(t, s, scanner.Int64, StatusDropped)

	// a dropped type cannot be used any more
	if _, err := s.Render(scanner.Int64, b); !errors.Is(err, ErrTypeNotActive) {
		t.Fatalf("render of a dropped type: %v", err)
	}

	if !s.UndoScan() {
		t.Fatal("nothing to undo")
	}
	for _, typ := range []scanner.Type{scanner.Int8, scanner.Int32, scanner.Int64} {
		requireStatus(t, s, typ, StatusOK)
	}
	if got := s.Scan(scanner.Int8).Count(); got != smallBefore {
		t.Fatalf("Int8 has %d results after undo, had %d", got, smallBefore)
	}
	if got := area.hits(s, scanner.Int64); !reflect.DeepEqual(got, bAddrs) {
		t.Fatalf("Int64 after undo: %d hits, want %d", len(got), len(bAddrs))
	}
}

func TestSessionNextScanPrunesEmptyTypes(t *testing.T) {
	area := newPlantedArea(t, 100*4096)
	x, b := scanner.NewInt32(0x6F1E9A3C), scanner.NewInt64(0x1D2C3B4A5F607182)
	nowhere := scanner.NewInt32(0x2B3C4D5E) // matches none of the results
	xAddrs := area.plant(x, slots(30, 0)...)
	bAddrs := area.plant(b, slots(30, 1)...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, x, b)
	xBefore := s.Scan(scanner.Int32).Count()

	mustNextScan(t, s, nowhere, b)
	requireStatus(t, s, scanner.Int32, StatusEmpty)
	requireStatus(t, s, scanner.Int64, StatusOK)
	if n := s.Scan(scanner.Int32).Count(); n != 0 {
		t.Fatalf("pruned type still holds %d results", n)
	}

	if !s.UndoScan() {
		t.Fatal("nothing to undo")
	}
	requireStatus(t, s, scanner.Int32, StatusOK)
	if n := s.Scan(scanner.Int32).Count(); n != xBefore {
		t.Fatalf("Int32 has %d results after undo, had %d", n, xBefore)
	}
	if got := area.hits(s, scanner.Int32); !reflect.DeepEqual(got, xAddrs) {
		t.Fatalf("Int32 after undo: %d hits, want %d", len(got), len(xAddrs))
	}
	if got := area.hits(s, scanner.Int64); !reflect.DeepEqual(got, bAddrs) {
		t.Fatalf("Int64 after undo: %d hits, want %d", len(got), len(bAddrs))
	}
}

func TestSessionStartingANewScanClosesTheUndoWindow(t *testing.T) {
	area := newPlantedArea(t, 100*4096)
	x, b := scanner.NewInt32(0x6F1E9A3C), scanner.NewInt64(0x1D2C3B4A5F607182)
	area.plant(x, slots(30, 0)...)
	area.plant(b, slots(30, 1)...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, x, b)
	mustNextScan(t, s, x) // drops Int64
	requireStatus(t, s, scanner.Int64, StatusDropped)
	mustNextScan(t, s, x) // Int64 is final now, its memory is released

	if n := s.Scan(scanner.Int64).Count(); n != 0 {
		t.Fatalf("a type dropped two scans ago still holds %d results", n)
	}
	if s.Rounds() != 3 {
		t.Fatalf("round %d, want 3", s.Rounds())
	}
	if !s.UndoScan() {
		t.Fatal("the last scan must be undoable")
	}
	requireStatus(t, s, scanner.Int64, StatusDropped) // not brought back
	requireStatus(t, s, scanner.Int32, StatusOK)
	if s.UndoScan() {
		t.Fatal("only one level of undo")
	}
}

// more results than nextScanSparseThreshold: the dense (virtual regions) path
func TestSessionNextScanDensePath(t *testing.T) {
	const count = 40000
	area := newPlantedArea(t, 160*4096)
	x, x2 := scanner.NewInt32(0x6F1E9A3C), scanner.NewInt32(0x71AB2C05)

	var offsets, changed []int
	for k := 0; k < count; k++ {
		offsets = append(offsets, k*16)
		if k%40 == 0 {
			changed = append(changed, k*16)
		}
	}
	area.plant(x, offsets...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, x)
	if n := len(area.hits(s, scanner.Int32)); n != count {
		t.Fatalf("first scan: %d hits in the area, want %d", n, count)
	}
	if s.Scan(scanner.Int32).Count() <= nextScanSparseThreshold {
		t.Skip("not enough results to reach the dense path")
	}

	want := area.plant(x2, changed...)
	mustNextScan(t, s, x2)
	if got := area.hits(s, scanner.Int32); !reflect.DeepEqual(got, want) {
		t.Fatalf("%d hits, want %d", len(got), len(want))
	}
}

func TestSessionChangeWriteAndRender(t *testing.T) {
	area := newPlantedArea(t, 50*4096)
	x := scanner.NewInt32(randomInt32())
	addrs := area.plant(x, slots(20, 0)...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, x)

	// positions of the planted addresses in the result list
	results := s.Scan(scanner.Int32).Results()
	var indexes []int
	for _, addr := range addrs {
		i := sort.Search(len(results), func(i int) bool { return results[i] >= addr })
		if i == len(results) || results[i] != addr {
			t.Fatalf("planted address %#x is not in the results", addr)
		}
		indexes = append(indexes, i)
	}

	readInt32 := func(addr uint64) uint32 {
		b := area.mem[addr-area.start:]
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	}

	if err := s.ChangeValues(scanner.Int32, scanner.NewInt32(0x01020304), indexes); err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		if got := readInt32(addr); got != 0x01020304 {
			t.Fatalf("%#x holds %#x after ChangeValues", addr, got)
		}
	}

	if err := s.WriteAddresses(scanner.Int32, scanner.NewInt32(0x0A0B0C0D), addrs[:5]); err != nil {
		t.Fatal(err)
	}
	for i, addr := range addrs {
		want := uint32(0x01020304)
		if i < 5 {
			want = 0x0A0B0C0D
		}
		if got := readInt32(addr); got != want {
			t.Fatalf("%#x holds %#x, want %#x", addr, got, want)
		}
	}

	rows, err := s.Render(scanner.Int32, scanner.NewInt32(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != s.Scan(scanner.Int32).Count() {
		t.Fatalf("%d rows for %d results", len(rows), s.Scan(scanner.Int32).Count())
	}
	wantRow := [2]string{fmt.Sprintf("%08X", addrs[10]), "16909060"} // 0x01020304
	found := false
	for _, row := range rows {
		if row == wantRow {
			found = true
		}
	}
	if !found {
		t.Fatalf("row %v not rendered", wantRow)
	}
}

func TestSessionRenderRefusesLongLists(t *testing.T) {
	area := newPlantedArea(t, 10*4096)
	x := scanner.NewInt32(randomInt32())
	var offsets []int
	for k := 0; k < IOV_MAX+100; k++ {
		offsets = append(offsets, k*8)
	}
	area.plant(x, offsets...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, x)
	if _, err := s.Render(scanner.Int32, x); !errors.Is(err, ErrTooManyResults) {
		t.Fatalf("render of %d results: %v", s.Scan(scanner.Int32).Count(), err)
	}
}

func TestSessionNextScanAfterCancel(t *testing.T) {
	area := newPlantedArea(t, 50*4096)
	x := scanner.NewInt32(0x6F1E9A3C)
	addrs := area.plant(x, slots(20, 0)...)

	s, _ := newSelfSession(t)
	mustFirstScan(t, s, x)
	s.Cancel() // e.g. the user canceled a scan that was running
	mustNextScan(t, s, x)
	if got := area.hits(s, scanner.Int32); !reflect.DeepEqual(got, addrs) {
		t.Fatalf("%d hits after a canceled session, want %d", len(got), len(addrs))
	}
}

func TestSessionNextScanRejectsBadRequests(t *testing.T) {
	s, _ := newSelfSession(t)
	x := scanner.NewInt32(1)

	if _, err := s.NextScan([]*scanner.Value{x}); !errors.Is(err, ErrNoActiveType) {
		t.Errorf("before any first scan: %v", err)
	}
	if err := s.ChangeValues(scanner.Int32, x, nil); !errors.Is(err, ErrTypeNotActive) {
		t.Errorf("ChangeValues on an unknown type: %v", err)
	}
	if _, err := NewSession().NextScan([]*scanner.Value{x}); !errors.Is(err, ErrSessionNotOpen) {
		t.Errorf("closed session: %v", err)
	}

	area := newPlantedArea(t, 10*4096)
	area.plant(scanner.NewInt32(0x6F1E9A3C), slots(5, 0)...)
	mustFirstScan(t, s, scanner.NewInt32(0x6F1E9A3C))
	dup := []*scanner.Value{scanner.NewInt32(1), scanner.NewInt32(2)}
	if _, err := s.NextScan(dup); !errors.Is(err, ErrDuplicateType) {
		t.Errorf("duplicate type: %v", err)
	}
	if s.Rounds() != 1 || s.CanUndo() {
		t.Errorf("a rejected request changed the session: round %d, canUndo %v", s.Rounds(), s.CanUndo())
	}
}
