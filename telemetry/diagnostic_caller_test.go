package telemetry_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/domainry/domainry-foundation/telemetry"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

var registerDiagnosticCallerDriver sync.Once

func TestObservedSQLCallerIsBusinessCallSite(t *testing.T) {
	previousPolicy := telemetry.CurrentDiagnosticLogPolicy()
	if err := telemetry.ConfigureDiagnosticLogging(telemetry.DiagnosticLogPolicy{
		Enabled: true, Features: []string{telemetry.DiagnosticFeatureSQL}, MaxContentBytes: 1024,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := telemetry.ConfigureDiagnosticLogging(previousPolicy); err != nil {
			t.Errorf("restore policy: %v", err)
		}
	})

	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core, zap.AddCaller()))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })

	const driverName = "domainry_foundation_diagnostic_caller_test"
	registerDiagnosticCallerDriver.Do(func() { sql.Register(driverName, diagnosticCallerDriver{}) })
	database, err := telemetry.OpenObservedSQL(driverName, "", "runtime", telemetry.NewSQLMetrics())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := businessSQLExec(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	entries := observed.FilterMessage("dependency_call").All()
	if len(entries) != 1 {
		t.Fatalf("entries=%#v", observed.All())
	}
	caller := entries[0].Caller
	if !caller.Defined || !strings.HasSuffix(caller.File, "diagnostic_caller_test.go") || !strings.Contains(caller.Function, "businessSQLExec") {
		t.Fatalf("caller=%+v, want businessSQLExec in diagnostic_caller_test.go", caller)
	}
	if strings.Contains(caller.File, "diagnostic_logging.go") || strings.Contains(caller.Function, "RecordDependencyCall") {
		t.Fatalf("caller points at diagnostic wrapper: %+v", caller)
	}
}

func businessSQLExec(ctx context.Context, database *sql.DB) error {
	_, err := database.ExecContext(ctx, "UPDATE widgets SET state = ?", "ready")
	return err
}

type diagnosticCallerDriver struct{}

func (diagnosticCallerDriver) Open(string) (driver.Conn, error) { return diagnosticCallerConn{}, nil }

type diagnosticCallerConn struct{}

func (diagnosticCallerConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (diagnosticCallerConn) Close() error                        { return nil }
func (diagnosticCallerConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (diagnosticCallerConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (diagnosticCallerConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return diagnosticCallerRows{}, nil
}

type diagnosticCallerRows struct{}

func (diagnosticCallerRows) Columns() []string         { return []string{"id"} }
func (diagnosticCallerRows) Close() error              { return nil }
func (diagnosticCallerRows) Next([]driver.Value) error { return io.EOF }
