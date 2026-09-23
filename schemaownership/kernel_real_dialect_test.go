package schemaownership_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func TestSharedKernelMigrationsExecuteOnRealDialects(t *testing.T) {
	required := strings.EqualFold(strings.TrimSpace(os.Getenv("DOMAINRY_FOUNDATION_REQUIRE_REAL_DIALECTS")), "true")
	for _, test := range []struct {
		name, sqlDriver, dsnEnvironment string
	}{
		{name: "sqlite", sqlDriver: "sqlite"},
		{name: "postgres", sqlDriver: "pgx", dsnEnvironment: "RUNTIME_POSTGRES_TEST_DSN"},
		{name: "mysql", sqlDriver: "mysql", dsnEnvironment: "RUNTIME_MYSQL_TEST_DSN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := ""
			if test.name == "sqlite" {
				dsn = filepath.Join(t.TempDir(), "foundation-contract.db")
			} else {
				dsn = strings.TrimSpace(os.Getenv(test.dsnEnvironment))
				if dsn == "" {
					if required {
						t.Fatalf("%s is required", test.dsnEnvironment)
					}
					t.Skipf("%s is not configured", test.dsnEnvironment)
				}
			}
			database, schema := openIsolatedContractDatabase(t, test.name, test.sqlDriver, dsn)
			for _, kernel := range sharedKernelContracts {
				migrations, err := kernel.migrations(test.name, schema)
				if err != nil {
					t.Fatalf("build %s migrations: %v", kernel.name, err)
				}
				for _, migration := range migrations {
					for _, statement := range migration.Statements {
						if _, err := database.ExecContext(t.Context(), statement); err != nil {
							t.Fatalf("execute %s migration %d/%s: %v\n%s", kernel.name, migration.Version, migration.Name, err, statement)
						}
					}
				}
			}
			for _, table := range sharedKernelTables() {
				if !realTableExists(t.Context(), t, database, test.name, schema, table) {
					t.Fatalf("%s schema omitted %s", test.name, table)
				}
			}
		})
	}
}

func openIsolatedContractDatabase(t *testing.T, dialect, sqlDriver, dsn string) (*sql.DB, string) {
	t.Helper()
	if dialect == "sqlite" {
		database, err := sql.Open(sqlDriver, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		return database, ""
	}
	schema := fmt.Sprintf("foundation_contract_%d", time.Now().UnixNano())
	if dialect == "mysql" {
		configuration, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("parse MySQL DSN: %v", err)
		}
		configuration.DBName = ""
		admin, err := sql.Open(sqlDriver, configuration.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.PingContext(t.Context()); err != nil {
			_ = admin.Close()
			t.Fatalf("connect MySQL administrator: %v", err)
		}
		if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE `"+schema+"`"); err != nil {
			_ = admin.Close()
			t.Fatal(err)
		}
		configuration.DBName = schema
		database, err := sql.Open(sqlDriver, configuration.FormatDSN())
		if err != nil {
			_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+schema+"`")
			_ = admin.Close()
			t.Fatal(err)
		}
		if err := database.PingContext(t.Context()); err != nil {
			_ = database.Close()
			_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+schema+"`")
			_ = admin.Close()
			t.Fatalf("connect isolated MySQL database: %v", err)
		}
		t.Cleanup(func() {
			_ = database.Close()
			_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+schema+"`")
			_ = admin.Close()
		})
		return database, schema
	}
	database, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PingContext(t.Context()); err != nil {
		_ = database.Close()
		t.Fatalf("connect %s: %v", dialect, err)
	}
	t.Cleanup(func() { _ = database.Close() })
	switch dialect {
	case "postgres":
		if _, err := database.ExecContext(t.Context(), `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = database.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
		})
	default:
		t.Fatalf("unsupported contract dialect %q", dialect)
	}
	return database, schema
}

func realTableExists(ctx context.Context, t *testing.T, database *sql.DB, dialect, schema, table string) bool {
	t.Helper()
	var count int
	var err error
	switch dialect {
	case "sqlite":
		err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	case "postgres":
		err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2`, schema, table).Scan(&count)
	case "mysql":
		err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=? AND table_name=?`, schema, table).Scan(&count)
	default:
		t.Fatalf("unsupported contract dialect %q", dialect)
	}
	if err != nil {
		t.Fatal(err)
	}
	return count == 1
}
