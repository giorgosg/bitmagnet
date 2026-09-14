package concurrency

import (
	"sync"
	"time"
)

type BatchingChannel[T any] interface {
	In() chan<- T
	Out() <-chan []T
	// Close stops batching. Everything already queued or buffered is sent on Out
	// in batches no larger than any other, and Out is then closed; In is left
	// open, so a late sender neither panics nor blocks while there is room.
	// Calling it more than once is harmless. A caller that closes must keep
	// reading Out until it is closed, because the final flush has nowhere else
	// to go.
	Close()
}

type batchingChannel[T any] struct {
	input        chan T
	output       chan []T
	closing      chan struct{}
	closeOnce    sync.Once
	buffer       []T
	maxBatchSize int
	maxWaitTime  time.Duration
	ticker       *time.Ticker
}

func NewBatchingChannel[T any](capacity int, maxBatchSize int, maxWaitTime time.Duration) BatchingChannel[T] {
	ch := &batchingChannel[T]{
		input:        make(chan T, capacity),
		output:       make(chan []T, 1),
		closing:      make(chan struct{}),
		maxBatchSize: maxBatchSize,
		maxWaitTime:  maxWaitTime,
		ticker:       time.NewTicker(maxWaitTime),
	}
	go ch.batch()

	return ch
}

func (ch *batchingChannel[T]) In() chan<- T {
	return ch.input
}

func (ch *batchingChannel[T]) Out() <-chan []T {
	return ch.output
}

func (ch *batchingChannel[T]) Close() {
	ch.closeOnce.Do(func() {
		close(ch.closing)
	})
}

func (ch *batchingChannel[T]) batch() {
	defer close(ch.output)
	defer ch.ticker.Stop()

	for {
		select {
		case next, ok := <-ch.input:
			if !ok {
				ch.drain()

				return
			}

			ch.buffer = append(ch.buffer, next)
			if len(ch.buffer) >= ch.maxBatchSize {
				ch.flush()
			}
		case <-ch.ticker.C:
			if len(ch.buffer) > 0 {
				ch.flush()
			}
		case <-ch.closing:
			ch.drain()

			return
		}
	}
}

// drain takes everything still queued on the input and sends the lot on, so a
// shutdown loses neither the buffer nor what had been handed over but not yet
// picked up.
func (ch *batchingChannel[T]) drain() {
	for {
		select {
		case next := <-ch.input:
			ch.buffer = append(ch.buffer, next)
		default:
			for len(ch.buffer) > 0 {
				ch.flush()
			}

			return
		}
	}
}

func (ch *batchingChannel[T]) flush() {
	ch.ticker.Stop()

	size := min(len(ch.buffer), ch.maxBatchSize)
	batch := ch.buffer[:size:size]
	ch.buffer = ch.buffer[size:]

	ch.ticker.Reset(ch.maxWaitTime)

	ch.output <- batch
}
