package lsp

import "sync"

// ringBuffer keeps the last n bytes written to it. Language servers log to
// stderr without a size limit; FastExec keeps a bounded tail so a chatty
// server cannot grow the daemon.
type ringBuffer struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max}
}

func (r *ringBuffer) reset() {
	r.mu.Lock()
	r.data = nil
	r.mu.Unlock()
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if len(r.data) > r.max {
		r.data = r.data[len(r.data)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.data)
}
