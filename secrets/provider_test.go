package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileProviderConfinesReadsToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "db"), []byte(" dsn "), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := FileProvider{Root: root}
	value, err := provider.Resolve(t.Context(), "db", "")
	if err != nil || string(value) != "dsn" {
		t.Fatalf("resolve file secret: value=%q err=%v", value, err)
	}
	if _, err := provider.Resolve(t.Context(), "../outside", ""); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("path escape was not rejected: %v", err)
	}
}
