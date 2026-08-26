package telemetry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

var observedSQLDriverSequence atomic.Uint64

func OpenObservedSQL(driverName, dataSourceName, role string, metrics *SQLMetrics) (*sql.DB, error) {
	probe, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	underlying := probe.Driver()
	_ = probe.Close()
	name := fmt.Sprintf("domainry_observed_%s_%d", driverName, observedSQLDriverSequence.Add(1))
	sql.Register(name, observedDriver{driver: underlying, role: role, metrics: metrics})
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
	return observedStmt{Stmt: stmt, role: c.role, operation: sqlOperation(query), metrics: c.metrics, conn: c}, nil
}

func (c *observedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if contextual, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := contextual.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return observedStmt{Stmt: stmt, role: c.role, operation: sqlOperation(query), metrics: c.metrics, conn: c}, nil
	}
	return c.Prepare(query)
}

func (c *observedConn) Begin() (driver.Tx, error) {
	started := time.Now()
	tx, err := c.Conn.Begin()
	if err != nil {
		c.metrics.ObserveTransaction(c.role, "error", time.Since(started), err)
		return nil, err
	}
	c.inTransaction.Store(true)
	return &observedTx{Tx: tx, role: c.role, started: started, metrics: c.metrics, onFinish: func() { c.inTransaction.Store(false) }}, nil
}

func (c *observedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	started := time.Now()
	if contextual, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err := contextual.BeginTx(ctx, opts)
		if err != nil {
			c.metrics.ObserveTransaction(c.role, "error", time.Since(started), err)
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
			if err := c.transactionInitializer(ctx, execer); err != nil {
				_ = tx.Rollback()
				c.inTransaction.Store(false)
				c.metrics.ObserveTransaction(c.role, "error", time.Since(started), err)
				return nil, err
			}
		}
		return &observedTx{Tx: tx, role: c.role, started: started, metrics: c.metrics, onFinish: func() { c.inTransaction.Store(false) }}, nil
	}
	return c.Begin()
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
	return rows, err
}

func (c *observedConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		started := time.Now()
		err := pinger.Ping(ctx)
		c.metrics.ObserveQuery(c.role, "other", time.Since(started), err)
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
	return rows, err
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
	return rows, err
}

type observedTx struct {
	driver.Tx
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
		if t.onFinish != nil {
			t.onFinish()
		}
	}
	return err
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
