package mediamodel

import "slices"

type Queue struct {
	items        []QueueItem
	currentIndex int
}

func NewQueue(items []QueueItem, currentIndex int) *Queue {
	if len(items) == 0 {
		return nil
	}
	if currentIndex < -1 || currentIndex >= len(items) {
		currentIndex = 0
	}
	return &Queue{items: slices.Clone(items), currentIndex: currentIndex}
}

func (q *Queue) Clone() *Queue {
	if q == nil {
		return nil
	}
	return NewQueue(q.items, q.currentIndex)
}

func (q *Queue) Items() []QueueItem {
	if q == nil {
		return nil
	}
	return slices.Clone(q.items)
}

func (q *Queue) Len() int {
	if q == nil {
		return 0
	}
	return len(q.items)
}

func (q *Queue) CurrentIndex() int {
	if q == nil {
		return -1
	}
	return q.currentIndex
}

func (q *Queue) SetCurrentIndex(index int) bool {
	if q == nil || index < -1 || index >= len(q.items) {
		return false
	}
	q.currentIndex = index
	return true
}

func (q *Queue) Item(index int) (QueueItem, bool) {
	if q == nil || index < 0 || index >= len(q.items) {
		return QueueItem{}, false
	}
	return q.items[index], true
}

func (q *Queue) Current() (QueueItem, bool) { return q.Item(q.CurrentIndex()) }

func (q *Queue) IndexByPath(path string) int {
	if q == nil {
		return -1
	}
	return slices.IndexFunc(q.items, func(item QueueItem) bool { return item.Path() == path })
}

func (q *Queue) SetCurrentByPath(path string) bool {
	index := q.IndexByPath(path)
	if index < 0 {
		return false
	}
	q.currentIndex = index
	return true
}

func (q *Queue) AdjacentIndex(delta int, sameTypeOnly, wrap bool) int {
	if q == nil || delta == 0 || q.currentIndex < 0 || q.currentIndex >= len(q.items) {
		return -1
	}

	targetKind := q.items[q.currentIndex].MediaKind()
	matches := func(index int) bool { return !sameTypeOnly || q.items[index].MediaKind() == targetKind }
	for index := q.currentIndex + delta; index >= 0 && index < len(q.items); index += delta {
		if matches(index) {
			return index
		}
	}
	if !wrap {
		return -1
	}

	if delta > 0 {
		for index := 0; index < q.currentIndex; index++ {
			if matches(index) {
				return index
			}
		}
	} else {
		for index := len(q.items) - 1; index > q.currentIndex; index-- {
			if matches(index) {
				return index
			}
		}
	}
	return -1
}

func (q *Queue) Move(index, delta int) int {
	if q == nil || index < 0 || index >= len(q.items) {
		return -1
	}
	target := index + delta
	if target < 0 || target >= len(q.items) {
		return index
	}

	item := q.items[index]
	if index < target {
		copy(q.items[index:target], q.items[index+1:target+1])
	} else {
		copy(q.items[target+1:index+1], q.items[target:index])
	}
	q.items[target] = item
	switch {
	case q.currentIndex == index:
		q.currentIndex = target
	case index < q.currentIndex && q.currentIndex <= target:
		q.currentIndex--
	case target <= q.currentIndex && q.currentIndex < index:
		q.currentIndex++
	}
	return target
}

func (q *Queue) Remove(index int) (QueueItem, bool) {
	if q == nil || index < 0 || index >= len(q.items) {
		return QueueItem{}, false
	}
	removed := q.items[index]
	q.items = slices.Delete(q.items, index, index+1)
	switch {
	case len(q.items) == 0:
		q.currentIndex = -1
	case q.currentIndex == index:
		q.currentIndex = -1
	case q.currentIndex > index:
		q.currentIndex--
	}
	return removed, true
}
