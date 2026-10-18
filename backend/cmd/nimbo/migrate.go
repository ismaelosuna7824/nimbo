package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// legacyHomeDir is the data directory used before the product was renamed
// from "IaC Cloud" to Nimbo. Only used by the one-time migration below.
const legacyHomeDir = ".iac-cloud"

// migrateLegacyHome renames <userHome>/.iac-cloud to home when home does not
// exist yet. It reports whether the rename happened.
func migrateLegacyHome(userHome, home string) (bool, error) {
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	legacy := filepath.Join(userHome, legacyHomeDir)
	if fi, err := os.Stat(legacy); err != nil || !fi.IsDir() {
		return false, nil
	}
	if err := os.Rename(legacy, home); err != nil {
		return false, err
	}
	return true, nil
}
