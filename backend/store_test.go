package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSharedKeyGateIsDurableAndHourly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	create := func(id string, at time.Time) (bool, time.Time) {
		t.Helper()
		allowed, next, err := store.Create(newRun(id, "acme/widget", 1, "ai"), true, at)
		if err != nil {
			t.Fatal(err)
		}
		return allowed, next
	}
	if allowed, _ := create("first", now); !allowed {
		t.Fatal("first shared-key run denied")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if allowed, next := create("second", now.Add(59*time.Minute)); allowed || !next.Equal(now.Add(time.Hour)) {
		t.Fatalf("gate returned allowed=%v next=%v", allowed, next)
	}
	if allowed, _ := create("third", now.Add(time.Hour)); !allowed {
		t.Fatal("shared key should be available after one hour")
	}
	runs, err := store.List(10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("denied run was saved: count=%d err=%v", len(runs), err)
	}
}

func TestSharedKeyGateIsAtomic(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	results := make(chan bool, 2)
	for _, id := range []string{"one", "two"} {
		go func(id string) {
			allowed, _, err := store.Create(newRun(id, "acme/widget", 1, "ai"), true, now)
			if err != nil {
				t.Errorf("create: %v", err)
			}
			results <- allowed
		}(id)
	}
	if first, second := <-results, <-results; first == second {
		t.Fatalf("expected one accepted request, got %v and %v", first, second)
	}
}
