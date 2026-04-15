package master

import (
	"os"
	"path/filepath"
	"testing"

	"cracker/Common/security"
)

func TestWriteOutputFileRepairsExistingPermissions(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(security.EnvDataDir, dataDir)
	t.Setenv(security.EnvAllowUnsafePaths, "")

	path := filepath.Join(dataDir, "cracked.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile setup: %v", err)
	}
	if err := writeOutputFile(path, []string{"secret"}); err != nil {
		t.Fatalf("writeOutputFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("output file permissions still broad: %o", info.Mode().Perm())
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(bytes) != "secret" {
		t.Fatalf("unexpected output content %q", string(bytes))
	}
}
