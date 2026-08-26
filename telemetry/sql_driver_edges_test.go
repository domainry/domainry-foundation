package telemetry

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

var errTelemetryDriver = errors.New("telemetry driver failure")

type telemetryResult int64

func (r telemetryResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r telemetryResult) RowsAffected() (int64, error) { return int64(r), nil }

type telemetryRows struct{}

func (telemetryRows) Columns() []string         { return []string{"value"} }
func (telemetryRows) Close() error              { return nil }
func (telemetryRows) Next([]driver.Value) error { return io.EOF }

type telemetryTx struct {
	commits, rollbacks int
	err                error
}

func (t *telemetryTx) Commit() error   { t.commits++; return t.err }
func (t *telemetryTx) Rollback() error { t.rollbacks++; return t.err }

type telemetryStmt struct{ err error }

func (s telemetryStmt) Close() error  { return nil }
func (s telemetryStmt) NumInput() int { return -1 }
func (s telemetryStmt) Exec([]driver.Value) (driver.Result, error) {
	return telemetryResult(1), s.err
}
func (s telemetryStmt) Query([]driver.Value) (driver.Rows, error) {
	return telemetryRows{}, s.err
}
func (s telemetryStmt) ExecContext(context.Context, []driver.NamedValue) (driver.Result, error) {
	return telemetryResult(1), s.err
}
func (s telemetryStmt) QueryContext(context.Context, []driver.NamedValue) (driver.Rows, error) {
	return telemetryRows{}, s.err
}

type telemetryLegacyStmt struct{}

func (telemetryLegacyStmt) Close() error  { return nil }
func (telemetryLegacyStmt) NumInput() int { return -1 }
func (telemetryLegacyStmt) Exec([]driver.Value) (driver.Result, error) {
	return telemetryResult(1), nil
}
func (telemetryLegacyStmt) Query([]driver.Value) (driver.Rows, error) {
	return telemetryRows{}, nil
}

type telemetryConn struct {
	err      error
	tx       *telemetryTx
	valid    bool
	checked  bool
	reset    bool
	pinged   bool
	executed bool
	queried  bool
}

func (c *telemetryConn) Prepare(string) (driver.Stmt, error) {
	if c.err != nil {
		return nil, c.err
	}
	return telemetryStmt{}, nil
}
func (c *telemetryConn) Close() error { return nil }
func (c *telemetryConn) Begin() (driver.Tx, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.tx == nil {
		c.tx = &telemetryTx{}
	}
	return c.tx, nil
}
func (c *telemetryConn) PrepareContext(context.Context, string) (driver.Stmt, error) {
	return c.Prepare("")
}
func (c *telemetryConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *telemetryConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.executed = true
	return telemetryResult(1), c.err
}
func (c *telemetryConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.queried = true
	return telemetryRows{}, c.err
}
func (c *telemetryConn) Ping(context.Context) error { c.pinged = true; return c.err }
func (c *telemetryConn) CheckNamedValue(*driver.NamedValue) error {
	c.checked = true
	return c.err
}
func (c *telemetryConn) ResetSession(context.Context) error { c.reset = true; return c.err }
func (c *telemetryConn) IsValid() bool                      { return c.valid }

type telemetryBareConn struct{ err error }

func (c telemetryBareConn) Prepare(string) (driver.Stmt, error) {
	return telemetryLegacyStmt{}, c.err
}
func (telemetryBareConn) Close() error { return nil }
func (c telemetryBareConn) Begin() (driver.Tx, error) {
	return &telemetryTx{}, c.err
}

type telemetryDriver struct {
	conn driver.Conn
	err  error
}

func (d telemetryDriver) Open(string) (driver.Conn, error) { return d.conn, d.err }

type telemetryContextDriver struct{ telemetryDriver }

func (d telemetryContextDriver) OpenConnector(string) (driver.Connector, error) {
	if d.err != nil {
		return nil, d.err
	}
	return telemetryConnector{driver: d.telemetryDriver, conn: d.conn}, nil
}

type telemetryConnector struct {
	driver driver.Driver
	conn   driver.Conn
	err    error
}

func (c telemetryConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, c.err }
func (c telemetryConnector) Driver() driver.Driver                        { return c.driver }

func TestObservedDriverConnectorAndConnectionInterfaces(t *testing.T) {
	metrics := NewSQLMetrics()
	base := &telemetryConn{valid: true}
	driverWrapper := observedDriver{driver: telemetryDriver{conn: base}, role: "runtime", metrics: metrics}
	opened, err := driverWrapper.Open("dsn")
	if err != nil {
		t.Fatal(err)
	}
	observed := opened.(*observedConn)
	if _, err := observed.Prepare("SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := observed.PrepareContext(t.Context(), "UPDATE x SET y=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := observed.ExecContext(t.Context(), "INSERT INTO x VALUES (1)", nil); err != nil || !base.executed {
		t.Fatalf("exec err=%v executed=%v", err, base.executed)
	}
	if _, err := observed.QueryContext(t.Context(), "SELECT 1", nil); err != nil || !base.queried {
		t.Fatalf("query err=%v queried=%v", err, base.queried)
	}
	if err := observed.Ping(t.Context()); err != nil || !base.pinged {
		t.Fatalf("ping err=%v pinged=%v", err, base.pinged)
	}
	if err := observed.CheckNamedValue(&driver.NamedValue{}); err != nil || !base.checked {
		t.Fatalf("check err=%v checked=%v", err, base.checked)
	}
	if err := observed.ResetSession(t.Context()); err != nil || !base.reset || !observed.IsValid() {
		t.Fatalf("reset err=%v reset=%v valid=%v", err, base.reset, observed.IsValid())
	}

	connector, err := driverWrapper.OpenConnector("dsn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Connect(t.Context()); err != nil || connector.Driver() == nil {
		t.Fatalf("fallback connector err=%v", err)
	}
	contextDriver := observedDriver{driver: telemetryContextDriver{telemetryDriver{conn: base}}, role: "migration", metrics: metrics}
	contextConnector, err := contextDriver.OpenConnector("dsn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contextConnector.Connect(t.Context()); err != nil || contextConnector.Driver() == nil {
		t.Fatalf("context connector err=%v", err)
	}

	if _, err := (observedDriver{driver: telemetryDriver{err: errTelemetryDriver}, metrics: metrics}).Open("dsn"); !errors.Is(err, errTelemetryDriver) {
		t.Fatalf("open error=%v", err)
	}
	if _, err := (observedDriver{driver: telemetryContextDriver{telemetryDriver{err: errTelemetryDriver}}, metrics: metrics}).OpenConnector("dsn"); !errors.Is(err, errTelemetryDriver) {
		t.Fatalf("open connector error=%v", err)
	}
	if _, err := (observedConnector{connector: telemetryConnector{err: errTelemetryDriver}, metrics: metrics}).Connect(t.Context()); !errors.Is(err, errTelemetryDriver) {
		t.Fatalf("connect error=%v", err)
	}
}

func TestObservedConnectionFallbacksTransactionsAndInitializers(t *testing.T) {
	metrics := NewSQLMetrics()
	bare := observedConn{Conn: telemetryBareConn{}, role: "runtime", metrics: metrics}
	if _, err := bare.PrepareContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := bare.ExecContext(t.Context(), "SELECT 1", nil); !errors.Is(err, driver.ErrSkip) {
		t.Fatalf("bare exec=%v", err)
	}
	if _, err := bare.QueryContext(t.Context(), "SELECT 1", nil); !errors.Is(err, driver.ErrSkip) {
		t.Fatalf("bare query=%v", err)
	}
	if err := bare.Ping(t.Context()); err != nil || bare.CheckNamedValue(&driver.NamedValue{}) != driver.ErrSkip || bare.ResetSession(t.Context()) != nil || !bare.IsValid() {
		t.Fatal("bare optional interface fallback mismatch")
	}
	if _, err := bare.BeginTx(t.Context(), driver.TxOptions{}); err != nil {
		t.Fatal(err)
	}

	failing := observedConn{Conn: &telemetryConn{err: errTelemetryDriver}, role: "runtime", metrics: metrics}
	if _, err := failing.Prepare("SELECT"); !errors.Is(err, errTelemetryDriver) {
		t.Fatalf("prepare error=%v", err)
	}
	if _, err := failing.PrepareContext(t.Context(), "SELECT"); !errors.Is(err, errTelemetryDriver) {
		t.Fatalf("prepare context error=%v", err)
	}
	if _, err := failing.Begin(); !errors.Is(err, errTelemetryDriver) {
		t.Fatalf("begin error=%v", err)
	}
	if _, err := failing.BeginTx(t.Context(), driver.TxOptions{}); !errors.Is(err, errTelemetryDriver) {
		t.Fatalf("begin tx error=%v", err)
	}

	base := &telemetryConn{valid: true}
	initialized := false
	withInitializer := observedConn{Conn: base, role: "runtime", metrics: metrics, transactionInitializer: func(context.Context, driver.ExecerContext) error {
		initialized = true
		return nil
	}}
	if _, err := withInitializer.BeginTx(t.Context(), driver.TxOptions{}); err != nil || !initialized {
		t.Fatalf("initializer err=%v called=%v", err, initialized)
	}
	base.tx = &telemetryTx{}
	withInitializer.transactionInitializer = func(context.Context, driver.ExecerContext) error { return errTelemetryDriver }
	if _, err := withInitializer.BeginTx(t.Context(), driver.TxOptions{}); !errors.Is(err, errTelemetryDriver) || base.tx.rollbacks != 1 {
		t.Fatalf("initializer failure=%v rollbacks=%d", err, base.tx.rollbacks)
	}
	withoutExecer := observedConn{Conn: telemetryContextBareConn{}, role: "runtime", metrics: metrics, transactionInitializer: func(context.Context, driver.ExecerContext) error { return nil }}
	if _, err := withoutExecer.BeginTx(t.Context(), driver.TxOptions{}); err == nil {
		t.Fatal("initializer accepted connection without ExecerContext")
	}
}

func TestTransactionInitializerWrapsDirectOperationsAndDoesNotNestExplicitTransactions(t *testing.T) {
	metrics := NewSQLMetrics()
	base := &telemetryConn{valid: true}
	initialized := 0
	guarded := &observedConn{Conn: base, role: "runtime", metrics: metrics, transactionInitializer: func(context.Context, driver.ExecerContext) error {
		initialized++
		return nil
	}}
	if _, err := guarded.ExecContext(t.Context(), "UPDATE records SET value=1", nil); err != nil {
		t.Fatal(err)
	}
	rows, err := guarded.QueryContext(t.Context(), "SELECT value FROM records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Next(make([]driver.Value, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("rows terminal error=%v", err)
	}
	if initialized != 2 || base.tx == nil || base.tx.commits != 2 || base.tx.rollbacks != 0 {
		t.Fatalf("direct operations were not individually guarded: initialized=%d tx=%+v", initialized, base.tx)
	}
	tx, err := guarded.BeginTx(t.Context(), driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guarded.ExecContext(t.Context(), "UPDATE records SET value=2", nil); err != nil {
		t.Fatal(err)
	}
	if initialized != 3 {
		t.Fatalf("explicit transaction nested initializer calls=%d", initialized)
	}
	if err := tx.Commit(); err != nil || guarded.inTransaction.Load() {
		t.Fatalf("explicit transaction finish err=%v active=%v", err, guarded.inTransaction.Load())
	}
}

func TestObservedTransactionRecordsOnlyFirstTerminalCall(t *testing.T) {
	metrics := NewSQLMetrics()
	underlying := &telemetryTx{}
	tx := &observedTx{Tx: underlying, role: "runtime", started: time.Now(), metrics: metrics}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if underlying.commits != 1 || underlying.rollbacks != 1 {
		t.Fatalf("terminal calls: commits=%d rollbacks=%d", underlying.commits, underlying.rollbacks)
	}
	output := metrics.OpenMetrics(t.Context())
	if !strings.Contains(output, `outcome="commit"`) || strings.Contains(output, `outcome="rollback"`) {
		t.Fatalf("transaction metrics recorded more than first terminal call:\n%s", output)
	}

	underlying = &telemetryTx{}
	tx = &observedTx{Tx: underlying, role: "runtime", started: time.Now(), metrics: NewSQLMetrics()}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type telemetryContextBareConn struct{ telemetryBareConn }

func (telemetryContextBareConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &telemetryTx{}, nil
}

func TestObservedStatementsAndConnectorConstructionEdges(t *testing.T) {
	metrics := NewSQLMetrics()
	stmt := observedStmt{Stmt: telemetryStmt{}, role: "runtime", operation: "select", metrics: metrics}
	if _, err := stmt.Exec(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.Query(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.ExecContext(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.QueryContext(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	legacy := observedStmt{Stmt: telemetryLegacyStmt{}, role: "runtime", operation: "other", metrics: metrics}
	if _, err := legacy.ExecContext(t.Context(), nil); !errors.Is(err, driver.ErrSkip) {
		t.Fatalf("legacy stmt exec=%v", err)
	}
	if _, err := legacy.QueryContext(t.Context(), nil); !errors.Is(err, driver.ErrSkip) {
		t.Fatalf("legacy stmt query=%v", err)
	}
	guarded := observedStmt{Stmt: telemetryStmt{}, role: "runtime", operation: "select", metrics: metrics, conn: &observedConn{transactionInitializer: func(context.Context, driver.ExecerContext) error { return nil }}}
	if _, err := guarded.Exec(nil); err == nil || !strings.Contains(err.Error(), "ExecContext") {
		t.Fatalf("guarded legacy exec did not fail closed: %v", err)
	}
	if _, err := guarded.Query(nil); err == nil || !strings.Contains(err.Error(), "QueryContext") {
		t.Fatalf("guarded legacy query did not fail closed: %v", err)
	}

	if WrapSQLConnector(nil, "runtime", metrics) != nil {
		t.Fatal("nil connector was wrapped")
	}
	connector := telemetryConnector{driver: telemetryDriver{}, conn: &telemetryConn{valid: true}}
	withoutInitializer := WrapSQLConnector(connector, "runtime", metrics)
	if withoutInitializer == nil {
		t.Fatal("connector wrapper without initializer missing")
	}
	wrapped := WrapSQLConnector(connector, "runtime", metrics, nil, func(context.Context, driver.ExecerContext) error { return errTelemetryDriver })
	if wrapped == nil || wrapped.Driver() == nil {
		t.Fatal("connector wrapper missing")
	}
	if _, err := wrapped.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenObservedSQL("missing-driver", "dsn", "runtime", metrics); err == nil {
		t.Fatal("unknown SQL driver accepted")
	}
}
