package secrets

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type MemoryKeyRing struct {
	mu       sync.RWMutex
	activeID string
	keys     map[string]Key
}

func NewMemoryKeyRing(active Key, decryptOnly ...Key) (*MemoryKeyRing, error) {
	active.ID = strings.TrimSpace(active.ID)
	if active.ID == "" || len(active.Material) == 0 {
		return nil, fmt.Errorf("active key id and material are required")
	}
	active.Status = KeyActive
	if active.Algorithm == "" {
		active.Algorithm = "AES-256-GCM"
	}
	ring := &MemoryKeyRing{activeID: active.ID, keys: map[string]Key{active.ID: cloneKey(active)}}
	for _, key := range decryptOnly {
		key.ID = strings.TrimSpace(key.ID)
		if key.ID == "" || len(key.Material) == 0 || key.ID == active.ID {
			return nil, fmt.Errorf("decrypt-only key id and material must be unique")
		}
		key.Status = KeyDecryptOnly
		if key.Algorithm == "" {
			key.Algorithm = "AES-256-GCM"
		}
		ring.keys[key.ID] = cloneKey(key)
	}
	return ring, nil
}

func (r *MemoryKeyRing) Active(ctx context.Context, _ string) (Key, error) {
	if err := ctx.Err(); err != nil {
		return Key{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.keys[r.activeID]
	if !ok || key.Status != KeyActive {
		return Key{}, ErrProviderUnavailable
	}
	return cloneKey(key), nil
}

func (r *MemoryKeyRing) ByID(ctx context.Context, _, id string) (Key, error) {
	if err := ctx.Err(); err != nil {
		return Key{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.keys[strings.TrimSpace(id)]
	if !ok {
		return Key{}, ErrSecretNotFound
	}
	if key.Status == KeyRevoked {
		return Key{}, ErrKeyRevoked
	}
	return cloneKey(key), nil
}

func (r *MemoryKeyRing) Rotate(next Key) error {
	next.ID = strings.TrimSpace(next.ID)
	if next.ID == "" || len(next.Material) == 0 {
		return fmt.Errorf("next key id and material are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.keys[r.activeID]; ok {
		previous.Status = KeyDecryptOnly
		r.keys[previous.ID] = previous
	}
	next.Status = KeyActive
	if next.Algorithm == "" {
		next.Algorithm = "AES-256-GCM"
	}
	r.keys[next.ID], r.activeID = cloneKey(next), next.ID
	return nil
}

func (r *MemoryKeyRing) Revoke(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == r.activeID {
		return fmt.Errorf("cannot revoke active key")
	}
	key, ok := r.keys[id]
	if !ok {
		return ErrSecretNotFound
	}
	key.Status = KeyRevoked
	key.Material = nil
	r.keys[id] = key
	return nil
}

func cloneKey(key Key) Key {
	key.Material = append([]byte(nil), key.Material...)
	return key
}
