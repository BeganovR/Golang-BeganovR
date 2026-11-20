package positions

import (
	"bytes"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type task struct {
	trip.StaleCandidate
	source trace.SpanContext
	index  int
}

func (t *task) before(other *task) bool {
	if cmp := t.LastSeenAt.Compare(other.LastSeenAt); cmp != 0 {
		return cmp < 0
	}
	return bytes.Compare(t.TripID[:], other.TripID[:]) < 0
}

type priorityQueue []*task

func (q priorityQueue) Len() int           { return len(q) }
func (q priorityQueue) Less(i, j int) bool { return q[i].before(q[j]) }
func (q priorityQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *priorityQueue) Push(item any) {
	t := item.(*task)
	t.index = len(*q)
	*q = append(*q, t)
}

func (q *priorityQueue) Pop() any {
	last := len(*q) - 1
	t := (*q)[last]
	t.index = -1
	(*q)[last] = nil
	*q = (*q)[:last]
	return t
}

func (q priorityQueue) latest() *task {
	newest := q[len(q)/2]
	for _, t := range q[len(q)/2+1:] {
		if newest.before(t) {
			newest = t
		}
	}
	return newest
}

type scheduled struct {
	result  string
	dropped uuid.UUID
}
