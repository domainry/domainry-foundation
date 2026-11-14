package telemetry

import (
	"runtime"
	"testing"
)

func TestDependencyInfrastructureFrameSkipsTrimmedStandardLibrarySQL(t *testing.T) {
	for _, frame := range []runtime.Frame{
		{Function: "database/sql.(*DB).query", File: "database/sql/sql.go", Line: 1759},
		{Function: "database/sql.ctxDriverExec", File: "database/sql/ctxutil.go", Line: 31},
		{Function: "database/sql/driver.ExecerContext.ExecContext", File: "database/sql/driver/driver.go", Line: 189},
	} {
		if !dependencyInfrastructureFrame(frame) {
			t.Fatalf("trimmed standard-library frame was treated as a business caller: %+v", frame)
		}
	}
}
