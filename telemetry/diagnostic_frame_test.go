package telemetry

import (
	"runtime"
	"testing"
)

func TestDependencyInfrastructureFrameSkipsTrimmedStandardLibrary(t *testing.T) {
	for _, frame := range []runtime.Frame{
		{Function: "database/sql.(*DB).query", File: "database/sql/sql.go", Line: 1759},
		{Function: "database/sql.ctxDriverExec", File: "database/sql/ctxutil.go", Line: 31},
		{Function: "database/sql/driver.ExecerContext.ExecContext", File: "database/sql/driver/driver.go", Line: 189},
		{Function: "sync.(*Once).doSlow", File: "sync/once.go", Line: 78},
		{Function: "net/http.(*Client).do", File: "net/http/client.go", Line: 725},
		{Function: "reflect.Value.Call", File: "reflect/value.go", Line: 365},
	} {
		if !dependencyInfrastructureFrame(frame) {
			t.Fatalf("trimmed standard-library frame was treated as a business caller: %+v", frame)
		}
	}
}

func TestDependencyInfrastructureFrameKeepsSourceOwnedCaller(t *testing.T) {
	for _, frame := range []runtime.Frame{
		{Function: "main.run", File: "cmd/server/main.go", Line: 42},
		{Function: "github.com/domainry/domainry-foundation/example.Store", File: "example/store.go", Line: 17},
		{Function: "example.org/product/repository.(*Store).Load", File: "repository/store.go", Line: 91},
	} {
		if dependencyInfrastructureFrame(frame) {
			t.Fatalf("source-owned frame was treated as infrastructure: %+v", frame)
		}
	}
}
