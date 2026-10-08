package consumer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileState persists a handler's state as one JSON file, written atomically
// (temp file + rename). The file lives on a volume owned by this consumer only.
// An empty path keeps state in memory (lost on restart).
type FileState struct{ path string }

func NewFileState(path string) FileState { return FileState{path: path} }

// Load fills v from the file; a missing file leaves v untouched.
func (s FileState) Load(v any) error {
	if s.path == "" {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state %s: %w", s.path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("decode state %s: %w", s.path, err)
	}
	return nil
}

func (s FileState) Save(v any) error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}
