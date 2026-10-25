package telemetry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var observedSQLDriverSequence atomic.Uint64

var observedSQLDriverRegistry = struct {
	sync.Mutex
	names map[string]string
}{names: map[string]string{}}

func OpenObservedSQL(driverName, dataSourceName, role string, metrics *SQLMetrics) (*sql.DB, error) {
	registryKey := strings.TrimSpace(driverName) + "\x00" + boundedSQLRole(role) + "\x00" + fmt.Sprintf("%p", metrics)
	observedSQLDriverRegistry.Lock()
	defer observedSQLDriverRegistry.Unlock()
	if name := observedSQLDriverRegistry.names[registryKey]; name != "" {
		return sql.Open(name, dataSourceName)
	}
	probe, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	underlying := probe.Driver()
	_ = probe.Close()
	name := fmt.Sprintf("domainry_observed_%s_%d", driverName, observedSQLDriverSequence.Add(1))
	sql.Register(name, observedDriver{driver: underlying, role: role, metrics: metrics})
	observedSQLDriverRegistry.names[registryKey] = name
	return sql.Open(name, dataSourceName)
}

// TransactionInitializer runs on the physical connection immediately after a
// transaction begins and before database/sql exposes the transaction.
type TransactionInitializer func(context.Context, driver.ExecerContext) error

func WrapSQLConnector(connector driver.Connector, role string, metrics *SQLMetrics, initializers ...TransactionInitializer) driver.Connector {
	if connector == nil {
		return nil
	}
	var initializer TransactionInitializer
	if len(initializers) > 0 {
		initializer = initializers[0]
	}
	return observedConnector{connector: connector, role: role, metrics: metrics, transactionInitializer: initializer}
}

type observedDriver struct {
	driver                 driver.Driver
	role                   string
	metrics                *SQLMetrics
	transactionInitializer TransactionInitializer
}

func (d observedDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &observedConn{Conn: conn, role: d.role, metrics: d.metrics, transactionInitializer: d.transactionInitializer}, nil
}

func (d observedDriver) OpenConnector(name string) (driver.Connector, error) {
	if contextual, ok := d.driver.(driver.DriverContext); ok {
		connector, err := contextual.OpenConnector(name)
		if err != nil {
			return nil, err
		}
		return observedConnector{connector: connector, role: d.role, metrics: d.metrics, transactionInitializer: d.transactionInitializer}, nil
	}
	return observedDriverConnector{driver: d, name: name}, nil
}

type observedDriverConnector struct {
	driver observedDriver
	name   string
}

func (c observedDriverConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open(c.name)
}
func (c observedDriverConnector) Driver() driver.Driver { return c.driver }

type observedConnector struct {
	connector              driver.Connector
	role                   string
	metrics                *SQLMetrics
	transactionInitializer TransactionInitializer
}

func (c observedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &observedConn{Conn: conn, role: c.role, metrics: c.metrics, transactionInitializer: c.transactionInitializer}, nil
}
func (c observedConnector) Driver() driver.Driver {
	return observedDriver{driver: c.connector.Driver(), role: c.role, metrics: c.metrics, transactionInitializer: c.transactionInitializer}
}

type observedConn struct {
	driver.Conn
	role                   string
	metrics                *SQLMetrics
	transactionInitializer TransactionInitializer
	inTransaction          atomic.Bool
}

func (c *observedConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return observedStmt{Stmt: stmt, role: c.role, operation: sqlOperation(query), statement: query, metrics: c.metrics, conn: c}, nil
}

func (c *observedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if contextual, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := contextual.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return observedStmt{Stmt: stmt, role: c.role, operation: sqlOperation(query), statement: query, metrics: c.metrics, conn: c}, nil
	}
	return c.Prepare(query)
}

func (c *observedConn) Begin() (driver.Tx, error) {
	started := time.Now()
	tx, err := c.Conn.Begin()
	if err != nil {
		c.metrics.ObserveTransaction(c.role, "error", time.Since(started), err)
		recordSQLTransaction(context.Background(), c.role, "begin", started, err)
		return nil, err
	}
	c.inTransaction.Store(true)
	return &observedTx{Tx: tx, ctx: context.Background(), role: c.role, started: started, metrics: c.metrics, onFinish: func() { c.inTransaction.Store(false) }}, nil
}

func (c *observedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	started := time.Now()
	if contextual, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err := contextual.BeginTx(ctx, opts)
		if err != nil {
			c.metrics.ObserveTransaction(c.role, "error", time.Since(started), err)
			recordSQLTransaction(ctx, c.role, "begin", started, err)
			return nil, err
		}
		c.inTransaction.Store(true)
		if c.transactionInitializer != nil {
			execer, ok := c.Conn.(driver.ExecerContext)
			if !ok {
				_ = tx.Rollback()
				c.inTransaction.Store(false)
				err = fmt.Errorf("transaction initializer requires driver.ExecerContext")
				c.metrics.ObserveTransaction(c.role, "error", time.Since(started), err)
				return nil, err
			}
			initializerExecer := observedInitializerExecer{ExecerContext: execer, role: c.role, metrics: c.metrics}
			if err := c.transactionInitializer(ctx, initializerExecer); err != nil {
				_ = tx.Rollback()
				c.inTransaction.Store(false)
				c.metrics.ObserveTransaction(c.role, "error", time.Since(started), err)
				return nil, err
			}
		}
		return &observedTx{Tx: tx, ctx: ctx, role: c.role, started: started, metrics: c.metrics, onFinish: func() { c.inTransaction.Store(false) }}, nil
	}
	return c.Begin()
}

type observedInitializerExecer struct {
	driver.ExecerContext
	role    string
	metrics *SQLMetrics
}

func (e observedInitializerExecer) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	started := time.Now()
	result, err := e.ExecerContext.ExecContext(ctx, query, args)
	e.metrics.ObserveQuery(e.role, sqlOperation(query), time.Since(started), err)
	recordSQLExecution(ctx, e.role, query, namedSQLDiagnosticArguments(args), sqlExecOutput(result), started, err)
	return result, err
}

func (c *observedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.transactionInitializer != nil && !c.inTransaction.Load() {
		tx, err := c.BeginTx(ctx, driver.TxOptions{})
		if err != nil {
			return nil, err
		}
		result, execErr := c.execContext(ctx, query, args)
		if execErr != nil {
			_ = tx.Rollback()
			return nil, execErr
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return result, nil
	}
	return c.execContext(ctx, query, args)
}

func (c *observedConn) execContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	result, err := execer.ExecContext(ctx, query, args)
	c.metrics.ObserveQuery(c.role, sqlOperation(query), time.Since(started), err)
	recordSQLExecution(ctx, c.role, query, namedSQLDiagnosticArguments(args), sqlExecOutput(result), started, err)
	return result, err
}

func (c *observedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.transactionInitializer != nil && !c.inTransaction.Load() {
		tx, err := c.BeginTx(ctx, driver.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		rows, queryErr := c.queryContext(ctx, query, args)
		if queryErr != nil {
			_ = tx.Rollback()
			return nil, queryErr
		}
		return &observedTransactionRows{Rows: rows, tx: tx}, nil
	}
	return c.queryContext(ctx, query, args)
}

func (c *observedConn) queryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	rows, err := queryer.QueryContext(ctx, query, args)
	c.metrics.ObserveQuery(c.role, sqlOperation(query), time.Since(started), err)
	if err != nil {
		recordSQLExecution(ctx, c.role, query, namedSQLDiagnosticArguments(args), nil, started, err)
		return rows, err
	}
	return newObservedSQLRows(rows, ctx, c.role, query, namedSQLDiagnosticArguments(args), started), nil
}

func (c *observedConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		started := time.Now()
		err := pinger.Ping(ctx)
		c.metrics.ObserveQuery(c.role, "other", time.Since(started), err)
		RecordDependencyCall(ctx, DependencyCall{Feature: DiagnosticFeatureSQL, Dependency: "database." + boundedSQLRole(c.role), Operation: "ping"}, DependencyResult{Err: err}, time.Since(started))
		return err
	}
	return nil
}

func (c *observedConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c *observedConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *observedConn) IsValid() bool {
	if validator, ok := c.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

type observedStmt struct {
	driver.Stmt
	role, operation string
	statement       string
	metrics         *SQLMetrics
	conn            *observedConn
}

func (s observedStmt) Exec(args []driver.Value) (driver.Result, error) {
	if s.conn != nil && s.conn.transactionInitializer != nil && !s.conn.inTransaction.Load() {
		return nil, errors.New("transaction initializer requires Stmt.ExecContext")
	}
	return s.exec(args)
}

func (s observedStmt) exec(args []driver.Value) (driver.Result, error) {
	started := time.Now()
	result, err := s.Stmt.Exec(args)
	s.metrics.ObserveQuery(s.role, s.operation, time.Since(started), err)
	recordSQLExecution(context.Background(), s.role, s.statement, positionalSQLDiagnosticArguments(args), sqlExecOutput(result), started, err)
	return result, err
}

func (s observedStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.conn != nil && s.conn.transactionInitializer != nil && !s.conn.inTransaction.Load() {
		return nil, errors.New("transaction initializer requires Stmt.QueryContext")
	}
	return s.query(args)
}

func (s observedStmt) query(args []driver.Value) (driver.Rows, error) {
	started := time.Now()
	rows, err := s.Stmt.Query(args)
	s.metrics.ObserveQuery(s.role, s.operation, time.Since(started), err)
	if err != nil {
		recordSQLExecution(context.Background(), s.role, s.statement, positionalSQLDiagnosticArguments(args), nil, started, err)
		return rows, err
	}
	return newObservedSQLRows(rows, context.Background(), s.role, s.statement, positionalSQLDiagnosticArguments(args), started), nil
}

func (s observedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if contextual, ok := s.Stmt.(driver.StmtExecContext); ok {
		if s.conn != nil && s.conn.transactionInitializer != nil && !s.conn.inTransaction.Load() {
			tx, err := s.conn.BeginTx(ctx, driver.TxOptions{})
			if err != nil {
				return nil, err
			}
			result, execErr := s.execContext(ctx, contextual, args)
			if execErr != nil {
				_ = tx.Rollback()
				return nil, execErr
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return result, nil
		}
		return s.execContext(ctx, contextual, args)
	}
	return nil, driver.ErrSkip
}

func (s observedStmt) execContext(ctx context.Context, contextual driver.StmtExecContext, args []driver.NamedValue) (driver.Result, error) {
	started := time.Now()
	result, err := contextual.ExecContext(ctx, args)
	s.metrics.ObserveQuery(s.role, s.operation, time.Since(started), err)
	recordSQLExecution(ctx, s.role, s.statement, namedSQLDiagnosticArguments(args), sqlExecOutput(result), started, err)
	return result, err
}

func (s observedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if contextual, ok := s.Stmt.(driver.StmtQueryContext); ok {
		if s.conn != nil && s.conn.transactionInitializer != nil && !s.conn.inTransaction.Load() {
			tx, err := s.conn.BeginTx(ctx, driver.TxOptions{ReadOnly: true})
			if err != nil {
				return nil, err
			}
			rows, queryErr := s.queryContext(ctx, contextual, args)
			if queryErr != nil {
				_ = tx.Rollback()
				return nil, queryErr
			}
			return &observedTransactionRows{Rows: rows, tx: tx}, nil
		}
		return s.queryContext(ctx, contextual, args)
	}
	return nil, driver.ErrSkip
}

func (s observedStmt) queryContext(ctx context.Context, contextual driver.StmtQueryContext, args []driver.NamedValue) (driver.Rows, error) {
	started := time.Now()
	rows, err := contextual.QueryContext(ctx, args)
	s.metrics.ObserveQuery(s.role, s.operation, time.Since(started), err)
	if err != nil {
		recordSQLExecution(ctx, s.role, s.statement, namedSQLDiagnosticArguments(args), nil, started, err)
		return rows, err
	}
	return newObservedSQLRows(rows, ctx, s.role, s.statement, namedSQLDiagnosticArguments(args), started), nil
}

type observedTx struct {
	driver.Tx
	ctx      context.Context
	role     string
	started  time.Time
	metrics  *SQLMetrics
	finished atomic.Bool
	onFinish func()
}

func (t *observedTx) Commit() error {
	err := t.Tx.Commit()
	if t.finished.CompareAndSwap(false, true) {
		t.metrics.ObserveTransaction(t.role, "commit", time.Since(t.started), err)
		recordSQLTransaction(t.ctx, t.role, "commit", t.started, err)
		if t.onFinish != nil {
			t.onFinish()
		}
	}
	return err
}

func (t *observedTx) Rollback() error {
	err := t.Tx.Rollback()
	if t.finished.CompareAndSwap(false, true) {
		t.metrics.ObserveTransaction(t.role, "rollback", time.Since(t.started), err)
		recordSQLTransaction(t.ctx, t.role, "rollback", t.started, err)
		if t.onFinish != nil {
			t.onFinish()
		}
	}
	return err
}

type observedSQLRows struct {
	driver.Rows
	ctx       context.Context
	role      string
	query     string
	arguments []sqlDiagnosticArgument
	started   time.Time
	rowCount  int64
	finished  atomic.Bool
}

func newObservedSQLRows(rows driver.Rows, ctx context.Context, role, query string, arguments []sqlDiagnosticArgument, started time.Time) driver.Rows {
	if rows == nil {
		return nil
	}
	return &observedSQLRows{
		Rows: rows, ctx: ctx, role: role, query: query,
		arguments: append([]sqlDiagnosticArgument(nil), arguments...), started: started,
	}
}

func (rows *observedSQLRows) Next(dest []driver.Value) error {
	err := rows.Rows.Next(dest)
	if err == nil {
		rows.rowCount++
		return nil
	}
	if errors.Is(err, io.EOF) {
		if next, ok := rows.Rows.(driver.RowsNextResultSet); ok && next.HasNextResultSet() {
			return err
		}
	}
	rows.finish(err, errors.Is(err, io.EOF))
	return err
}

func (rows *observedSQLRows) Close() error {
	err := rows.Rows.Close()
	rows.finish(err, false)
	return err
}

func (rows *observedSQLRows) HasNextResultSet() bool {
	if next, ok := rows.Rows.(driver.RowsNextResultSet); ok {
		return next.HasNextResultSet()
	}
	return false
}

func (rows *observedSQLRows) NextResultSet() error {
	if next, ok := rows.Rows.(driver.RowsNextResultSet); ok {
		err := next.NextResultSet()
		if err != nil {
			rows.finish(err, errors.Is(err, io.EOF))
		}
		return err
	}
	return io.EOF
}

func (rows *observedSQLRows) ColumnTypeScanType(index int) reflect.Type {
	if typed, ok := rows.Rows.(driver.RowsColumnTypeScanType); ok {
		return typed.ColumnTypeScanType(index)
	}
	return reflect.TypeFor[any]()
}

func (rows *observedSQLRows) ColumnTypeDatabaseTypeName(index int) string {
	if typed, ok := rows.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return typed.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}

func (rows *observedSQLRows) ColumnTypeLength(index int) (int64, bool) {
	if typed, ok := rows.Rows.(driver.RowsColumnTypeLength); ok {
		return typed.ColumnTypeLength(index)
	}
	return 0, false
}

func (rows *observedSQLRows) ColumnTypeNullable(index int) (bool, bool) {
	if typed, ok := rows.Rows.(driver.RowsColumnTypeNullable); ok {
		return typed.ColumnTypeNullable(index)
	}
	return false, false
}

func (rows *observedSQLRows) ColumnTypePrecisionScale(index int) (int64, int64, bool) {
	if typed, ok := rows.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return typed.ColumnTypePrecisionScale(index)
	}
	return 0, 0, false
}

func (rows *observedSQLRows) finish(err error, complete bool) {
	if !rows.finished.CompareAndSwap(false, true) {
		return
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	columns := append([]string(nil), rows.Rows.Columns()...)
	recordSQLExecution(rows.ctx, rows.role, rows.query, rows.arguments, map[string]any{
		"columns": columns, "row_count": rows.rowCount, "complete": complete,
	}, rows.started, err)
}

func recordSQLExecution(ctx context.Context, role, query string, arguments []sqlDiagnosticArgument, output map[string]any, started time.Time, err error) {
	argumentTypes := sqlDiagnosticArgumentTypes(arguments)
	input := map[string]any{
		"argument_count": len(argumentTypes),
		"argument_types": append([]string(nil), argumentTypes...),
	}
	policy := currentDiagnosticPolicy()
	statement := strings.TrimSpace(renderExecutableSQL(query, arguments))
	if policy.SQLStatement && statement != "" {
		input["statement_bytes"] = len(statement)
		if len(statement) <= policy.MaxContentBytes {
			input["statement"] = statement
		} else {
			digest := sha256.Sum256([]byte(statement))
			input["statement_sha256"] = hex.EncodeToString(digest[:])
			input["statement_omitted"] = "size_limit"
		}
	}
	RecordDependencyCall(ctx, DependencyCall{
		Feature: DiagnosticFeatureSQL, Dependency: "database." + boundedSQLRole(role), Operation: sqlOperation(query), Input: input,
	}, DependencyResult{Output: output, Err: err}, time.Since(started))
}

func recordSQLTransaction(ctx context.Context, role, operation string, started time.Time, err error) {
	RecordDependencyCall(ctx, DependencyCall{
		Feature: DiagnosticFeatureSQL, Dependency: "database." + boundedSQLRole(role), Operation: "transaction." + operation,
	}, DependencyResult{Err: err}, time.Since(started))
}

type sqlDiagnosticArgument struct {
	name  string
	value any
}

func namedSQLDiagnosticArguments(args []driver.NamedValue) []sqlDiagnosticArgument {
	result := make([]sqlDiagnosticArgument, 0, len(args))
	for _, argument := range args {
		result = append(result, sqlDiagnosticArgument{name: argument.Name, value: argument.Value})
	}
	return result
}

func positionalSQLDiagnosticArguments(args []driver.Value) []sqlDiagnosticArgument {
	result := make([]sqlDiagnosticArgument, 0, len(args))
	for _, argument := range args {
		result = append(result, sqlDiagnosticArgument{value: argument})
	}
	return result
}

func sqlDiagnosticArgumentTypes(arguments []sqlDiagnosticArgument) []string {
	result := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		result = append(result, fmt.Sprintf("%T", argument.value))
	}
	return result
}

func sqlExecOutput(result driver.Result) map[string]any {
	if result == nil {
		return nil
	}
	output := map[string]any{}
	if affected, err := result.RowsAffected(); err == nil {
		output["rows_affected"] = affected
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

type observedTransactionRows struct {
	driver.Rows
	tx       driver.Tx
	finished atomic.Bool
}

func (r *observedTransactionRows) Close() error {
	closeErr := r.Rows.Close()
	return r.finish(closeErr)
}

func (r *observedTransactionRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err == nil {
		return nil
	}
	return r.finish(err)
}

func (r *observedTransactionRows) finish(rowsErr error) error {
	if !r.finished.CompareAndSwap(false, true) {
		return rowsErr
	}
	if rowsErr != nil && !errors.Is(rowsErr, io.EOF) {
		_ = r.tx.Rollback()
		return rowsErr
	}
	if err := r.tx.Commit(); err != nil {
		return err
	}
	return rowsErr
}
