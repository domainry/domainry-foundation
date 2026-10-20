package logcontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRejectsHumanAndDynamicEvents(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "events.go")
	source := `package probe
import (
  "context"
  "github.com/domainry/domainry-foundation/logging"
)
func emit(ctx context.Context, event string, err error) {
  logger := logging.FromContext(ctx)
  logger.Info("valid_event")
  logger.Warn("Human event")
  logger.Error(event)
  logging.RecordFailure(ctx, "also-valid", err, logging.Failure{})
}`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	violations, err := CheckFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 3 {
		t.Fatalf("violations=%v", violations)
	}
	joined := violations[0].String() + violations[1].String() + violations[2].String()
	for _, expected := range []string{"Human event", "source-owned string literal", "also-valid"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %s", expected, joined)
		}
	}
}
