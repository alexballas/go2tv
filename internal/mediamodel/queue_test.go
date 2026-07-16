package mediamodel

import "testing"

func testItems(t *testing.T, paths ...string) []QueueItem {
	t.Helper()
	items := make([]QueueItem, 0, len(paths))
	for _, path := range paths {
		item, ok := NewQueueItem(path)
		if !ok {
			t.Fatalf("invalid media path %q", path)
		}
		items = append(items, item)
	}
	return items
}

func TestQueueSnapshotAndCloneIsolation(t *testing.T) {
	items := testItems(t, "/tmp/a.mp4", "/tmp/b.mp3")
	queue := NewQueue(items, 1)
	snapshot := queue.Items()
	snapshot[0] = snapshot[1]
	clone := queue.Clone()
	clone.Move(0, 1)
	first, _ := queue.Item(0)
	if first.Path() != "/tmp/a.mp4" || queue.CurrentIndex() != 1 {
		t.Fatal("snapshot or clone mutated original")
	}
}

func TestQueueCurrentRemoval(t *testing.T) {
	queue := NewQueue(testItems(t, "/tmp/a.mp4", "/tmp/b.mp4", "/tmp/c.mp4"), 1)
	removed, ok := queue.Remove(1)
	if !ok || removed.Path() != "/tmp/b.mp4" {
		t.Fatalf("removed = %q, %v", removed.Path(), ok)
	}
	if queue.CurrentIndex() != -1 || queue.Len() != 2 {
		t.Fatalf("current = %d len = %d", queue.CurrentIndex(), queue.Len())
	}
}

func TestQueueRemoveBeforeCurrentPreservesCurrentItem(t *testing.T) {
	queue := NewQueue(testItems(t, "/tmp/a.mp4", "/tmp/b.mp4", "/tmp/c.mp4"), 2)
	current, _ := queue.Current()
	queue.Remove(0)
	after, _ := queue.Current()
	if queue.CurrentIndex() != 1 || after.ID() != current.ID() {
		t.Fatal("current item not preserved")
	}
}

func TestQueueMovePreservesCurrentItemAndBoundaries(t *testing.T) {
	queue := NewQueue(testItems(t, "/tmp/a.mp4", "/tmp/b.mp4", "/tmp/c.mp4"), 1)
	current, _ := queue.Current()
	if got := queue.Move(0, -1); got != 0 {
		t.Fatalf("upper boundary = %d", got)
	}
	if got := queue.Move(2, 1); got != 2 {
		t.Fatalf("lower boundary = %d", got)
	}
	if got := queue.Move(1, 1); got != 2 {
		t.Fatalf("move = %d", got)
	}
	after, _ := queue.Current()
	if queue.CurrentIndex() != 2 || after.ID() != current.ID() {
		t.Fatal("move changed current item")
	}
}

func TestQueueMoveAcrossMultipleItems(t *testing.T) {
	queue := NewQueue(testItems(t, "/tmp/a.mp4", "/tmp/b.mp4", "/tmp/c.mp4", "/tmp/d.mp4"), 1)
	current, _ := queue.Current()
	if got := queue.Move(3, -3); got != 0 {
		t.Fatalf("move = %d", got)
	}
	items := queue.Items()
	for index, want := range []string{"/tmp/d.mp4", "/tmp/a.mp4", "/tmp/b.mp4", "/tmp/c.mp4"} {
		if items[index].Path() != want {
			t.Fatalf("item %d = %q, want %q", index, items[index].Path(), want)
		}
	}
	after, _ := queue.Current()
	if queue.CurrentIndex() != 2 || after.ID() != current.ID() {
		t.Fatal("move changed current item")
	}
	if got := queue.Move(0, 3); got != 3 {
		t.Fatalf("reverse move = %d", got)
	}
	for index, want := range []string{"/tmp/a.mp4", "/tmp/b.mp4", "/tmp/c.mp4", "/tmp/d.mp4"} {
		item, _ := queue.Item(index)
		if item.Path() != want {
			t.Fatalf("restored item %d = %q, want %q", index, item.Path(), want)
		}
	}
}

func TestQueueAdjacentBoundariesWrapAndSameType(t *testing.T) {
	queue := NewQueue(testItems(t, "/tmp/a.mp4", "/tmp/b.mp3", "/tmp/c.mp4"), 2)
	tests := []struct {
		name     string
		delta    int
		sameType bool
		wrap     bool
		want     int
	}{
		{name: "end", delta: 1, want: -1},
		{name: "wrap", delta: 1, wrap: true, want: 0},
		{name: "previous", delta: -1, want: 1},
		{name: "previous same type", delta: -1, sameType: true, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := queue.AdjacentIndex(tt.delta, tt.sameType, tt.wrap); got != tt.want {
				t.Fatalf("index = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestQueueIndexUsesFirstDuplicate(t *testing.T) {
	queue := NewQueue(testItems(t, "/tmp/a.mp4", "/tmp/a.mp4"), 1)
	if got := queue.IndexByPath("/tmp/a.mp4"); got != 0 {
		t.Fatalf("index = %d, want 0", got)
	}
}
