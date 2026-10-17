// Package factory builds models with fake data for tests and seeders:
//
//	var Users = factory.New(func(f *factory.Faker) models.User {
//		return models.User{Name: f.Name(), Email: f.Email()}
//	})
//
//	admins, err := Users.Count(3).State(func(u *models.User) { u.Role = "admin" }).Create(ctx, db)
//
// Factories are values: State, Count, Seed and AfterCreate return copies,
// so a base factory can be shared. Make builds values in memory; Create
// also inserts them through orm.Create. A [Faker] seeded per factory makes
// the data reproducible (Seed), which seeders and golden tests rely on.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md#factories-and-seeders.
package factory

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"slices"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
)

// Factory builds T values.
type Factory[T any] struct {
	def    func(f *Faker) T
	states []func(*T)
	after  []func(ctx context.Context, ex database.Executor, m *T) error
	count  int
	seed   uint64
	seeded bool
}

// New returns a factory whose definition returns one model with fake data.
func New[T any](def func(f *Faker) T) Factory[T] {
	return Factory[T]{def: def, count: 1}
}

// State applies fn to every built model, after the definition (and after
// earlier states).
func (f Factory[T]) State(fn func(*T)) Factory[T] {
	f.states = append(slices.Clip(f.states), fn)
	return f
}

// Count sets how many models Make and Create build.
func (f Factory[T]) Count(n int) Factory[T] {
	f.count = max(n, 0)
	return f
}

// Seed makes the fake data reproducible.
func (f Factory[T]) Seed(seed uint64) Factory[T] {
	f.seed, f.seeded = seed, true
	return f
}

// AfterCreate runs fn for every created model, inside Create; use it to
// create related models:
//
//	Users.AfterCreate(func(ctx context.Context, ex database.Executor, u *User) error {
//		_, err := Posts.Count(2).State(func(p *Post) { p.UserID = u.ID }).Create(ctx, ex)
//		return err
//	})
func (f Factory[T]) AfterCreate(fn func(ctx context.Context, ex database.Executor, m *T) error) Factory[T] {
	f.after = append(slices.Clip(f.after), fn)
	return f
}

// Make builds Count models without saving them.
func (f Factory[T]) Make() []T {
	seed := f.seed
	if !f.seeded {
		var b [8]byte
		_, _ = rand.Read(b[:]) // never fails since Go 1.24
		seed = binary.LittleEndian.Uint64(b[:])
	}

	fk := NewFaker(seed)
	out := make([]T, f.count)

	for i := range out {
		fk.seq = i + 1
		out[i] = f.def(fk)

		for _, st := range f.states {
			st(&out[i])
		}
	}

	return out
}

// MakeOne builds one model without saving it.
func (f Factory[T]) MakeOne() T { return f.Count(1).Make()[0] }

// Create builds and inserts Count models (in a transaction when ex is a
// *database.DB), then runs AfterCreate hooks.
func (f Factory[T]) Create(ctx context.Context, ex database.Executor) ([]T, error) {
	items := f.Make()

	err := database.RunInTx(ctx, ex, func(tx *database.Tx) error {
		for i := range items {
			if err := orm.Create(ctx, tx, &items[i]); err != nil {
				return fmt.Errorf("factory: create %T: %w", items[i], err)
			}

			for _, fn := range f.after {
				if err := fn(ctx, tx, &items[i]); err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return items, nil
}

// CreateOne builds and inserts one model.
func (f Factory[T]) CreateOne(ctx context.Context, ex database.Executor) (T, error) {
	items, err := f.Count(1).Create(ctx, ex)
	if err != nil {
		var zero T
		return zero, err
	}

	return items[0], nil
}
