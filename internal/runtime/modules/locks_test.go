package modules

import (
	"sync"
	"testing"
	"time"
)

func TestLockManagerAllowsOnlyOneConcurrentOwner(t *testing.T) {
	manager := newLockManager()
	const workers = 64
	var wg sync.WaitGroup
	successes := make(chan lockRecord, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if record, ok := manager.acquire("memory:rooms:room.123", NewLockOwnerID(), time.Second); ok {
				successes <- record
			}
		}()
	}
	wg.Wait()
	close(successes)

	var got []lockRecord
	for record := range successes {
		got = append(got, record)
	}
	if len(got) != 1 {
		t.Fatalf("acquired locks = %d, want 1", len(got))
	}
}

func TestLockManagerExpiredOwnerCannotReleaseNewToken(t *testing.T) {
	manager := newLockManager()
	first, ok := manager.acquire("device:serial.rpi", "a", time.Millisecond)
	if !ok {
		t.Fatal("first acquire failed")
	}
	time.Sleep(5 * time.Millisecond)
	second, ok := manager.acquire("device:serial.rpi", "b", time.Second)
	if !ok {
		t.Fatal("second acquire after expiry failed")
	}
	if first.token == second.token {
		t.Fatal("tokens should be unique")
	}
	if manager.release(first.key, first.token) {
		t.Fatal("expired first token released second lock")
	}
	if !manager.release(second.key, second.token) {
		t.Fatal("second token did not release current lock")
	}
}

func TestLockManagerRefreshRequiresCurrentToken(t *testing.T) {
	manager := newLockManager()
	first, ok := manager.acquire("memory:counter", "a", time.Millisecond)
	if !ok {
		t.Fatal("first acquire failed")
	}
	time.Sleep(5 * time.Millisecond)
	second, ok := manager.acquire("memory:counter", "b", time.Second)
	if !ok {
		t.Fatal("second acquire after expiry failed")
	}
	if _, ok := manager.refresh(first.key, first.token, time.Second); ok {
		t.Fatal("expired first token refreshed second lock")
	}
	if _, ok := manager.refresh(second.key, second.token, time.Second); !ok {
		t.Fatal("second token did not refresh current lock")
	}
}
