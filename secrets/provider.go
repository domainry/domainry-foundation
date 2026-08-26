package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	fileProviderAbs      = filepath.Abs
	fileProviderReadFile = os.ReadFile
)

var (
	ErrProviderUnavailable = errors.New("secret provider unavailable")
	ErrSecretNotFound      = errors.New("secret not found")
	ErrKeyRevoked          = errors.New("key revoked")
)

// SecretProvider is deliberately smaller than any cloud SDK. Implementations
// return material only for the duration of the caller's operation.
type SecretProvider interface {
	Resolve(ctx context.Context, name, version string) ([]byte, error)
}

type KeyStatus string

const (
	KeyActive      KeyStatus = "active"
	KeyDecryptOnly KeyStatus = "decrypt-only"
	KeyRevoked     KeyStatus = "revoked"
)

type Key struct {
	ID        string
	Algorithm string
	Status    KeyStatus
	Material  []byte
}

// KeyProvider exposes versioned data-encryption keys without leaking the
// backing KMS, Vault, or Secret Manager API into domain or persistence code.
type KeyProvider interface {
	Active(ctx context.Context, purpose string) (Key, error)
	ByID(ctx context.Context, purpose, id string) (Key, error)
}

type EnvProvider struct{ Prefix string }

func (p EnvProvider) Resolve(ctx context.Context, name, version string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := strings.ToUpper(strings.TrimSpace(p.Prefix + name))
	if strings.TrimSpace(version) != "" {
		key += "_" + strings.ToUpper(strings.TrimSpace(version))
	}
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return nil, fmt.Errorf("%w: %s", ErrSecretNotFound, name)
	}
	return []byte(value), nil
}

type FileProvider struct{ Root string }

func (p FileProvider) Resolve(ctx context.Context, name, version string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := fileProviderAbs(strings.TrimSpace(p.Root))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid root", ErrProviderUnavailable)
	}
	leaf := strings.TrimSpace(name)
	if strings.TrimSpace(version) != "" {
		leaf += "." + strings.TrimSpace(version)
	}
	path := filepath.Clean(filepath.Join(root, leaf))
	if path != root && !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return nil, fmt.Errorf("%w: invalid secret path", ErrSecretNotFound)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid root", ErrProviderUnavailable)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrSecretNotFound, name)
		}
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	if resolvedPath != resolvedRoot && !strings.HasPrefix(resolvedPath, resolvedRoot+string(os.PathSeparator)) {
		return nil, fmt.Errorf("%w: invalid secret path", ErrSecretNotFound)
	}
	value, err := fileProviderReadFile(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrSecretNotFound, name)
		}
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return []byte(strings.TrimSpace(string(value))), nil
}

// RemoteProvider is the production adapter seam. Deployments bind a KMS,
// Vault, or Secret Manager client without making its SDK part of Runtime.
type RemoteProvider struct {
	ResolveFunc func(context.Context, string, string) ([]byte, error)
}

type RemoteKeyProvider struct {
	ActiveFunc func(context.Context, string) (Key, error)
	ByIDFunc   func(context.Context, string, string) (Key, error)
}

func (p RemoteKeyProvider) Active(ctx context.Context, purpose string) (Key, error) {
	if p.ActiveFunc == nil {
		return Key{}, ErrProviderUnavailable
	}
	key, err := p.ActiveFunc(ctx, purpose)
	if err != nil {
		return Key{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return key, nil
}

func (p RemoteKeyProvider) ByID(ctx context.Context, purpose, id string) (Key, error) {
	if p.ByIDFunc == nil {
		return Key{}, ErrProviderUnavailable
	}
	key, err := p.ByIDFunc(ctx, purpose, id)
	if err != nil {
		return Key{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return key, nil
}

// Named production adapters preserve provider identity in composition while
// keeping cloud SDK types outside Runtime ports.
type KMSKeyProvider struct{ RemoteKeyProvider }
type VaultKeyProvider struct{ RemoteKeyProvider }
type SecretManagerKeyProvider struct{ RemoteKeyProvider }

func (p RemoteProvider) Resolve(ctx context.Context, name, version string) ([]byte, error) {
	if p.ResolveFunc == nil {
		return nil, ErrProviderUnavailable
	}
	value, err := p.ResolveFunc(ctx, name, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return value, nil
}
