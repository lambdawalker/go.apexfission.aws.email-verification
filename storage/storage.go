// Package storage defines durable atomic aggregate storage. Implementations must
// distinguish missing data, conflicts and unavailability and return independent bytes.
package storage

import (
	"context"
	"errors"
	"sync"
)

var ErrMissing = errors.New("record missing")
var ErrConflict = errors.New("concurrent update")

type Record struct {
	Version uint64
	Data    []byte
}
type Store interface {
	Load(context.Context, string) (Record, error)
	CompareAndSwap(context.Context, string, uint64, []byte) error
}

// Memory is for tests and demos only; it is not durable across process restarts.
type Memory struct {
	mu   sync.Mutex
	rows map[string]Record
}

func NewMemory() *Memory { return &Memory{rows: map[string]Record{}} }
func (m *Memory) Load(ctx context.Context, k string) (Record, error) {
	if e := ctx.Err(); e != nil {
		return Record{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[k]
	if !ok {
		return Record{}, ErrMissing
	}
	r.Data = append([]byte(nil), r.Data...)
	return r, nil
}
func (m *Memory) CompareAndSwap(ctx context.Context, k string, v uint64, b []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows[k].Version != v {
		return ErrConflict
	}
	m.rows[k] = Record{v + 1, append([]byte(nil), b...)}
	return nil
}
