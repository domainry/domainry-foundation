package telemetry

import (
	"database/sql"
	"strings"
	"testing"
)

func TestObservedSQLDriverCapturesQueriesTransactionsAndStableOutcomes(t *testing.T) {
	metrics := NewSQLMetrics()
	base := &telemetryConn{valid: true}
	const driverName = "domainry_foundation_telemetry_test"
	sql.Register(driverName, telemetryDriver{conn: base})
	database, err := OpenObservedSQL(driverName, "test", "runtime", metrics)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE secret_customer (id TEXT PRIMARY KEY, password TEXT)`); err != nil {
		t.Fatal(err)
	}
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO secret_customer (id, password) VALUES (?, ?)`, "one", "do-not-export"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	base.err = fakeSQLError("unique constraint on password=another-secret")
	if _, err := database.ExecContext(t.Context(), `INSERT INTO secret_customer DEFAULT VALUES`); err == nil {
		t.Fatal("expected conflict")
	}
	base.err = nil
	tx, err = database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	output := metrics.OpenMetrics(t.Context())
	for _, expected := range []string{
		`domainry_db_query_duration_seconds_count{role="runtime",operation="ddl",outcome="success"} 1`,
		`operation="insert",outcome="conflict"`,
		`domainry_db_transaction_duration_seconds_count{role="runtime",operation="transaction",outcome="commit"} 1`,
		`domainry_db_transaction_duration_seconds_count{role="runtime",operation="transaction",outcome="rollback"} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{"secret_customer", "password", "do-not-export", "another-secret"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("SQL data leaked into metrics: %s", output)
		}
	}
}

func TestSQLMetricsClassifyDeadlockWithoutExportingErrorText(t *testing.T) {
	metrics := NewSQLMetrics()
	metrics.ObserveQuery("migration", "update", 0, fakeSQLError("deadlock detected on password=secret"))
	output := metrics.OpenMetrics(t.Context())
	if !strings.Contains(output, `role="migration",operation="update",outcome="deadlock"`) || strings.Contains(output, "password=secret") {
		t.Fatalf("unexpected deadlock metrics: %s", output)
	}
}

type fakeSQLError string

func (e fakeSQLError) Error() string { return string(e) }
