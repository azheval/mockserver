package fixtures

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Store struct{ root string }

func New(root string) *Store { return &Store{root: filepath.Clean(root)} }
func (s *Store) Open(name string) (io.ReadCloser, error) {
	if name == "" || filepath.IsAbs(name) {
		return nil, fmt.Errorf("fixture path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("fixture path escapes fixture root")
	}
	p := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("fixture path escapes fixture root")
	}
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("symlink fixtures are not allowed")
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("fixture must be a regular file")
	}
	return os.Open(p)
}
