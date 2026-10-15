package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWorkDir(t *testing.T) {
	valid := filepath.Join(t.TempDir(), ".")
	got, err := ValidateWorkDir(valid)
	if err != nil {
		t.Fatalf("validate existing directory: %v", err)
	}
	if want := filepath.Clean(valid); got != want {
		t.Fatalf("validated directory = %q, want %q", got, want)
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "empty", path: "", want: "required"},
		{name: "relative", path: "relative", want: "absolute"},
		{name: "missing", path: filepath.Join(t.TempDir(), "missing"), want: "unavailable"},
		{name: "file", path: file, want: "not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateWorkDir(tc.path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateWorkDir(%q) error = %v, want %q", tc.path, err, tc.want)
			}
		})
	}
}
