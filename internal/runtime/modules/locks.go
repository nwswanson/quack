package modules

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

var globalLocks = newLockManager()

type lockManager struct {
	mu    sync.Mutex
	locks map[string]lockRecord
}

type lockRecord struct {
	key       string
	owner     string
	token     string
	expiresAt time.Time
}

type locksModule struct {
	site  string
	owner string
}

type lockHandle struct {
	manager    *lockManager
	key        string
	managerKey string
	owner      string
	token      string
	expiresAt  time.Time
}

func NewLockOwnerID() string {
	return randomLockToken()
}

func NewLocksModule(site string, owner string) *starlarkstruct.Module {
	m := &locksModule{site: normalizedMemorySite(site), owner: owner}
	return &starlarkstruct.Module{
		Name: "locks",
		Members: starlark.StringDict{
			"acquire": starlark.NewBuiltin("locks.acquire", m.acquire),
			"hold":    starlark.NewBuiltin("locks.hold", m.acquire),
		},
	}
}

func newLockManager() *lockManager {
	return &lockManager{locks: map[string]lockRecord{}}
}

func (m *locksModule) acquire(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	ttlMS := 0
	waitMS := 0
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "ttl_ms", &ttlMS, "wait_ms?", &waitMS); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("%s: key is required", fn.Name())
	}
	if ttlMS <= 0 {
		return nil, fmt.Errorf("%s: ttl_ms must be positive", fn.Name())
	}
	if waitMS < 0 {
		return nil, fmt.Errorf("%s: wait_ms cannot be negative", fn.Name())
	}
	ctx, _ := thread.Local("context").(context.Context)
	deadline := time.Now().Add(time.Duration(waitMS) * time.Millisecond)
	managerKey := scopedLockKey(m.site, key)
	for {
		record, ok := globalLocks.acquire(managerKey, m.owner, time.Duration(ttlMS)*time.Millisecond)
		if ok {
			return &lockHandle{
				manager:    globalLocks,
				key:        key,
				managerKey: record.key,
				owner:      record.owner,
				token:      record.token,
				expiresAt:  record.expiresAt,
			}, nil
		}
		if waitMS == 0 || !time.Now().Before(deadline) {
			return starlark.None, nil
		}
		wait := time.Until(deadline)
		if wait > 5*time.Millisecond {
			wait = 5 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		if ctx == nil {
			<-timer.C
			continue
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *lockManager) acquire(key string, owner string, ttl time.Duration) (lockRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	current, exists := m.locks[key]
	if exists && current.expiresAt.After(now) {
		return lockRecord{}, false
	}
	record := lockRecord{
		key:       key,
		owner:     owner,
		token:     randomLockToken(),
		expiresAt: now.Add(ttl),
	}
	m.locks[key] = record
	return record, true
}

func (m *lockManager) release(key string, token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.locks[key]
	if !exists || current.token != token {
		return false
	}
	delete(m.locks, key)
	return true
}

func (m *lockManager) refresh(key string, token string, ttl time.Duration) (lockRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.locks[key]
	if !exists || current.token != token || !current.expiresAt.After(time.Now()) {
		return lockRecord{}, false
	}
	current.expiresAt = time.Now().Add(ttl)
	m.locks[key] = current
	return current, true
}

func (h *lockHandle) String() string        { return fmt.Sprintf("<lock %q>", h.key) }
func (h *lockHandle) Type() string          { return "lock" }
func (h *lockHandle) Freeze()               {}
func (h *lockHandle) Truth() starlark.Bool  { return starlark.True }
func (h *lockHandle) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable: lock") }

func (h *lockHandle) AttrNames() []string {
	return []string{"expires_at", "key", "owner", "refresh", "release", "token"}
}

func (h *lockHandle) Attr(name string) (starlark.Value, error) {
	switch name {
	case "key":
		return starlark.String(h.key), nil
	case "owner":
		return starlark.String(h.owner), nil
	case "token":
		return starlark.String(h.token), nil
	case "expires_at":
		return starlark.MakeInt64(h.expiresAt.UnixMilli()), nil
	case "release":
		return starlark.NewBuiltin("lock.release", h.release), nil
	case "refresh":
		return starlark.NewBuiltin("lock.refresh", h.refresh), nil
	default:
		return nil, nil
	}
}

func (h *lockHandle) release(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.Bool(h.manager.release(h.managerKey, h.token)), nil
}

func (h *lockHandle) refresh(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	ttlMS := 0
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "ttl_ms", &ttlMS); err != nil {
		return nil, err
	}
	if ttlMS <= 0 {
		return nil, fmt.Errorf("%s: ttl_ms must be positive", fn.Name())
	}
	record, ok := h.manager.refresh(h.managerKey, h.token, time.Duration(ttlMS)*time.Millisecond)
	if ok {
		h.expiresAt = record.expiresAt
	}
	return starlark.Bool(ok), nil
}

func scopedLockKey(site string, key string) string {
	return site + "\x00" + key
}

func randomLockToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}
