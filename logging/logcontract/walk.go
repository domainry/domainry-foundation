package logcontract

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// Check walks one or more repository roots and returns every contract
// violation. Generated evidence, vendored code and tests are excluded.
func Check(roots ...string) ([]Violation, error) {
	violations := []Violation{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				name := entry.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" || strings.Contains(filepath.ToSlash(path), "/docs/evidence/") {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			found, err := CheckFile(path)
			if err != nil {
				return err
			}
			violations = append(violations, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return violations, nil
}
