package graph

import "testing"

func TestDetectCycle(t *testing.T) {
	graph := New([]Task{
		{ID: "A", Deps: []string{"B"}},
		{ID: "B", Deps: []string{"C"}},
		{ID: "C", Deps: []string{"A"}},
	})

	cycle, hasCycle := graph.DetectCycle()
	if !hasCycle {
		t.Fatal("expected cycle")
	}
	if len(cycle) < 2 {
		t.Fatalf("unexpected cycle payload: %+v", cycle)
	}
}

func TestDetectCycleNoCycle(t *testing.T) {
	graph := New([]Task{
		{ID: "A", Deps: []string{"B"}},
		{ID: "B", Deps: []string{}},
	})

	_, hasCycle := graph.DetectCycle()
	if hasCycle {
		t.Fatal("did not expect cycle")
	}
}
