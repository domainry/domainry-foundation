package telemetry

import "testing"

func TestRenderExecutableSQLSupportsDomainryDriverPlaceholders(t *testing.T) {
	tests := []struct {
		name       string
		statement  string
		arguments  []sqlDiagnosticArgument
		executable string
	}{
		{
			name:       "mysql_and_sqlite_question_marks",
			statement:  "INSERT INTO customers(name, active, score, raw) VALUES (?, ?, ?, ?)",
			arguments:  []sqlDiagnosticArgument{{value: "O'Reilly"}, {value: true}, {value: 12.5}, {value: []byte{0x00, 0xff}}},
			executable: "INSERT INTO customers(name, active, score, raw) VALUES ('O''Reilly', TRUE, 12.5, X'00ff')",
		},
		{
			name:       "postgres_ordinals",
			statement:  "UPDATE customers SET name=$2 WHERE id=$1 AND note='$1' /* $2 */ AND body=$$ $1 $$",
			arguments:  []sqlDiagnosticArgument{{value: int64(42)}, {value: "new"}},
			executable: "UPDATE customers SET name='new' WHERE id=42 AND note='$1' /* $2 */ AND body=$$ $1 $$",
		},
		{
			name:       "sql_server_ordinals_and_names",
			statement:  "EXEC save_customer @p1, @name, @@ROWCOUNT",
			arguments:  []sqlDiagnosticArgument{{value: int64(7)}, {name: "name", value: "Alice"}},
			executable: "EXEC save_customer 7, 'Alice', @@ROWCOUNT",
		},
		{
			name:       "named_colon_and_postgres_cast",
			statement:  "SELECT :name::text, payload ?| array['name'] -- :name\n",
			arguments:  []sqlDiagnosticArgument{{name: "name", value: "Alice"}},
			executable: "SELECT 'Alice'::text, payload ?| array['name'] -- :name\n",
		},
		{
			name:       "postgres_json_question_operator",
			statement:  "SELECT payload ? $1 FROM events WHERE id=$2",
			arguments:  []sqlDiagnosticArgument{{value: "name"}, {value: int64(9)}},
			executable: "SELECT payload ? 'name' FROM events WHERE id=9",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if executable := renderExecutableSQL(test.statement, test.arguments); executable != test.executable {
				t.Fatalf("executable SQL:\n%s\nwant:\n%s", executable, test.executable)
			}
		})
	}
}
