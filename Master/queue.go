package main

import "time"

type taskQueueItem struct {
	taskID    string
	priority  int
	createdAt time.Time
	seq       int64
}

type taskQueue []*taskQueueItem

func (q taskQueue) Len() int { return len(q) }

func (q taskQueue) Less(i, j int) bool {
	if q[i].priority != q[j].priority {
		return q[i].priority > q[j].priority
	}
	if !q[i].createdAt.Equal(q[j].createdAt) {
		return q[i].createdAt.Before(q[j].createdAt)
	}
	return q[i].seq < q[j].seq
}

func (q taskQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

func (q *taskQueue) Push(x any) {
	*q = append(*q, x.(*taskQueueItem))
}

func (q *taskQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}
