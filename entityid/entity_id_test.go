package entityid

import (
	"errors"
	"strings"
	"testing"
)

func TestGeneratorProducesCanonicalEntityID(t *testing.T) {
	id, err := (Generator{Random: strings.NewReader("0123456789abcdef")}).New("workspace")
	if err != nil {
		t.Fatal(err)
	}
	if want := "workspace_30313233343536373839616263646566"; id != want {
		t.Fatalf("id=%q want=%q", id, want)
	}
	if err := Validate("workspace", id); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratorRejectsInvalidNameAndEntropyFailure(t *testing.T) {
	for _, prefix := range []string{"Workspace", "workflow_proc", "123role", "notificationx"} {
		if _, err := New(prefix); err == nil {
			t.Fatalf("invalid entity prefix %q was accepted", prefix)
		}
	}
	want := errors.New("entropy unavailable")
	if _, err := (Generator{Random: failingReader{err: want}}).New("role"); !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}

func TestDeriveProducesStableCanonicalEntityID(t *testing.T) {
	first, err := Derive("role", "identity.workspace-role", "workspace-a", "admin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Derive("role", "identity.workspace-role", "workspace-a", "admin")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Derive("role", "identity.workspace-role", "workspace-b", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == other {
		t.Fatalf("derived IDs first=%q second=%q other=%q", first, second, other)
	}
	if err := Validate("role", first); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveFramesComponentsWithoutDelimiterCollisions(t *testing.T) {
	combined, err := Derive("role", "identity.workspace-role", "workspace-a\x00admin")
	if err != nil {
		t.Fatal(err)
	}
	separate, err := Derive("role", "identity.workspace-role", "workspace-a", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if combined == separate {
		t.Fatalf("length-framed derived IDs collided: %q", combined)
	}
}

func TestValidateRejectsNonCanonicalEntityIDs(t *testing.T) {
	for _, value := range []string{
		"workspace_abc",
		"role_30313233343536373839616263646566",
		"workspace_3031323334353637383961626364656G",
		"workspace_3031323334353637383961626364656A",
	} {
		if err := Validate("workspace", value); err == nil {
			t.Fatalf("non-canonical ID %q was accepted", value)
		}
	}
}

type failingReader struct{ err error }

func (reader failingReader) Read([]byte) (int, error) { return 0, reader.err }
