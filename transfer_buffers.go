package main

import (
	"context"
	"io"
	"sync"
)

const transferBufferSize = 32 << 10
const maximumTransferBuffers = 128 << 20
const maximumHashBuffers = 32 << 20

type bufferBudget struct {
	mu                           sync.Mutex
	transfer, hash               int
	maximumTransfer, maximumHash int
	changed                      chan struct{}
}
type bufferSnapshot struct {
	Transfer        int `json:"transfer_bytes"`
	Hash            int `json:"hash_bytes"`
	MaximumTransfer int `json:"maximum_transfer_bytes"`
	MaximumHash     int `json:"maximum_hash_bytes"`
}
type bufferBudgetKey struct{}
type transferBufferKey struct{}
type transferBuffers struct {
	read, hash []byte
	budget     *bufferBudget
	once       sync.Once
}

func newBufferBudget() *bufferBudget {
	return &bufferBudget{maximumTransfer: maximumTransferBuffers, maximumHash: maximumHashBuffers, changed: make(chan struct{})}
}
func (b *bufferBudget) reserve() (*transferBuffers, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.transfer+transferBufferSize > b.maximumTransfer || b.hash+transferBufferSize > b.maximumHash {
		return nil, b.changed
	}
	b.transfer += transferBufferSize
	b.hash += transferBufferSize
	// The caller owns these reservations before allocating or starting a worker.
	return &transferBuffers{budget: b}, b.changed
}
func (b *bufferBudget) acquire(ctx context.Context) (*transferBuffers, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		buffers, changed := b.reserve()
		if buffers != nil {
			return buffers, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
func (b *bufferBudget) signal() <-chan struct{} { b.mu.Lock(); defer b.mu.Unlock(); return b.changed }
func (b *bufferBudget) snapshot() bufferSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bufferSnapshot{b.transfer, b.hash, b.maximumTransfer, b.maximumHash}
}
func (b *transferBuffers) release() {
	if b == nil || b.budget == nil {
		return
	}
	b.once.Do(func() {
		owner := b.budget
		owner.mu.Lock()
		defer owner.mu.Unlock()
		b.read, b.hash = nil, nil
		owner.transfer -= transferBufferSize
		owner.hash -= transferBufferSize
		close(owner.changed)
		owner.changed = make(chan struct{})
	})
}
func transferBufferFrom(ctx context.Context) *transferBuffers {
	b, _ := ctx.Value(transferBufferKey{}).(*transferBuffers)
	return b
}

// Hide WriterTo/ReaderFrom fast paths, which could allocate their own copy
// buffers. A single owner uses each reserved slice at a time.
type transferReader struct{ io.Reader }
type transferWriter struct{ io.Writer }

func copyTransfer(ctx context.Context, out io.Writer, in io.Reader, hash bool) (int64, error) {
	b := transferBufferFrom(ctx)
	if b == nil {
		return io.Copy(out, in)
	} // Standalone API retains its existing bound.
	buffer := &b.read
	if hash {
		buffer = &b.hash
	}
	if *buffer == nil {
		*buffer = make([]byte, transferBufferSize)
	}
	return io.CopyBuffer(transferWriter{out}, transferReader{in}, *buffer)
}
func copyTransferN(ctx context.Context, out io.Writer, in io.Reader, n int64, hash bool) (int64, error) {
	written, err := copyTransfer(ctx, out, io.LimitReader(in, n), hash)
	if written == n {
		return written, nil
	}
	if written < n && err == nil {
		err = io.EOF
	}
	return written, err
}
