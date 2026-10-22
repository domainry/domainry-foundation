package definition

import (
	"strings"
	"testing"
)

func TestMySQLDefinitionVersionNaturalKeyFitsInnoDBBudget(t *testing.T) {
	migrations, err := SchemaMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range migrations[0].Statements {
		if !strings.Contains(statement, "`installation_id` VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL") {
			t.Fatalf("installation identity must use a single-byte index key: %s", statement)
		}
	}
	const indexedBytes = 255 + 4*(64+128+255+255)
	if indexedBytes+8 > 3072 {
		t.Fatalf("definition version natural key plus physical primary key needs %d bytes", indexedBytes+8)
	}
}
