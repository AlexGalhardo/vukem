package database

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// Manager holds named connections. The default connection is "default".
type Manager struct {
	mu    sync.RWMutex
	conns map[string]*DB
}

// NewManager returns a Manager with db as the default connection.
func NewManager(db *DB) *Manager {
	return &Manager{conns: map[string]*DB{"default": db}}
}

// Default returns the default connection.
func (m *Manager) Default() *DB { return m.Conn("default") }

// Conn returns a named connection or nil.
func (m *Manager) Conn(name string) *DB {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.conns[name]
}

// Add registers a named connection.
func (m *Manager) Add(name string, db *DB) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, dup := m.conns[name]; dup {
		return fmt.Errorf("database: connection %q already exists", name)
	}

	m.conns[name] = db

	return nil
}

// Names lists connection names.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return slices.Sorted(maps.Keys(m.conns))
}

// Ping checks every connection.
func (m *Manager) Ping(ctx context.Context) error {
	var errs []error

	for _, name := range m.Names() {
		if err := m.Conn(name).Ping(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	return errors.Join(errs...)
}

// Close closes every connection.
func (m *Manager) Close() error {
	var errs []error

	for _, name := range m.Names() {
		errs = append(errs, m.Conn(name).Close())
	}

	return errors.Join(errs...)
}
