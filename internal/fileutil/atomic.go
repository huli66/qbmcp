package fileutil

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Write syncs a temporary file in the destination directory, then replaces path.
func Write(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "qbmcp-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// WriteJSON saves readable JSON through the same atomic replacement path.
func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return Write(path, append(data, '\n'))
}

// RemoveIfExists makes clearing a marker idempotent.
func RemoveIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
