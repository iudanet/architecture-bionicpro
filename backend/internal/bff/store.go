package bff

import (
	"sync"
	"time"
)

// TTLStore is an in-memory key-value store with per-entry expiry.
type TTLStore[T any] struct {
	mu    sync.Mutex
	items map[string]ttlItem[T]
	now   func() time.Time
}

type ttlItem[T any] struct {
	value     T
	expiresAt time.Time
}

// NewTTLStore creates an empty store.
func NewTTLStore[T any]() *TTLStore[T] {
	return &TTLStore[T]{items: make(map[string]ttlItem[T]), now: time.Now}
}

// Put stores value under key for ttl.
func (s *TTLStore[T]) Put(key string, value T, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.items[key] = ttlItem[T]{value: value, expiresAt: s.now().Add(ttl)}
}

// Get returns a non-expired value.
func (s *TTLStore[T]) Get(key string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[key]
	if !ok || !s.now().Before(it.expiresAt) {
		delete(s.items, key)
		var zero T
		return zero, false
	}
	return it.value, true
}

// Take returns a non-expired value and removes it (one-time use).
func (s *TTLStore[T]) Take(key string) (T, bool) {
	v, ok := s.Get(key)
	s.Delete(key)
	return v, ok
}

// Update replaces the value of an existing key keeping its expiry.
func (s *TTLStore[T]) Update(key string, value T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[key]; ok {
		it.value = value
		s.items[key] = it
	}
}

// Delete removes key.
func (s *TTLStore[T]) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
}

// gcLocked удаляет протухшие записи; вызывается на запись, чтобы память не росла бесконечно.
func (s *TTLStore[T]) gcLocked() {
	now := s.now()
	for k, it := range s.items {
		if !now.Before(it.expiresAt) {
			delete(s.items, k)
		}
	}
}
