package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTokensRejectsEmptyOrSharedTokens(t *testing.T) {
	adminToken := strings.Repeat("a", MinConfiguredTokenLength)
	workerToken := strings.Repeat("b", MinConfiguredTokenLength)
	if err := ValidateTokens(Tokens{Admin: adminToken, Worker: workerToken}); err != nil {
		t.Fatalf("ValidateTokens valid tokens: %v", err)
	}
	if err := ValidateTokens(Tokens{Admin: "", Worker: workerToken}); err == nil {
		t.Fatal("ValidateTokens accepted missing admin token")
	}
	if err := ValidateTokens(Tokens{Admin: adminToken, Worker: adminToken}); err == nil {
		t.Fatal("ValidateTokens accepted identical admin and worker tokens")
	}
}

func TestResolveWordlistPathRequiresDataDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(EnvDataDir, dataDir)
	t.Setenv(EnvAllowUnsafePaths, "")

	wordlists := filepath.Join(dataDir, "wordlists")
	if err := os.MkdirAll(wordlists, 0o700); err != nil {
		t.Fatalf("MkdirAll wordlists: %v", err)
	}
	wordlist := filepath.Join(wordlists, "list.txt")
	if err := os.WriteFile(wordlist, []byte("password\n"), 0o600); err != nil {
		t.Fatalf("WriteFile wordlist: %v", err)
	}

	resolved, err := ResolveWordlistPath(filepath.Join("wordlists", "list.txt"))
	if err != nil {
		t.Fatalf("ResolveWordlistPath inside data dir: %v", err)
	}
	if resolved != wordlist {
		t.Fatalf("expected %q, got %q", wordlist, resolved)
	}

	if _, err := ResolveWordlistPath(filepath.Join("..", "outside.txt")); err == nil {
		t.Fatal("ResolveWordlistPath accepted traversal outside data dir")
	}
}

func TestResolvePathRejectsSymlinkEscape(t *testing.T) {
	dataDir := t.TempDir()
	outsideDir := t.TempDir()
	t.Setenv(EnvDataDir, dataDir)
	t.Setenv(EnvAllowUnsafePaths, "")

	outsideFile := filepath.Join(outsideDir, "list.txt")
	if err := os.WriteFile(outsideFile, []byte("password\n"), 0o600); err != nil {
		t.Fatalf("WriteFile outside wordlist: %v", err)
	}
	linkPath := filepath.Join(dataDir, "link.txt")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := ResolveWordlistPath(linkPath); err == nil {
		t.Fatal("ResolveWordlistPath accepted symlink escape")
	}
	if _, err := ResolveOutputPath(linkPath); err == nil {
		t.Fatal("ResolveOutputPath accepted symlink escape")
	}
}

func TestUnsafePathOverrideAllowsOutsideDataDir(t *testing.T) {
	dataDir := t.TempDir()
	outsideDir := t.TempDir()
	t.Setenv(EnvDataDir, dataDir)
	t.Setenv(EnvAllowUnsafePaths, "1")

	outsideFile := filepath.Join(outsideDir, "list.txt")
	if err := os.WriteFile(outsideFile, []byte("password\n"), 0o600); err != nil {
		t.Fatalf("WriteFile outside wordlist: %v", err)
	}

	resolved, err := ResolveWordlistPath(outsideFile)
	if err != nil {
		t.Fatalf("ResolveWordlistPath with unsafe override: %v", err)
	}
	if resolved != outsideFile {
		t.Fatalf("expected %q, got %q", outsideFile, resolved)
	}
}

func TestWorkerTokenStoreBindsAndRevokesWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker_tokens.json")
	token, err := IssueWorkerToken(path, "worker-1", false)
	if err != nil {
		t.Fatalf("IssueWorkerToken: %v", err)
	}
	workerID, err := MatchWorkerToken(path, token)
	if err != nil {
		t.Fatalf("MatchWorkerToken: %v", err)
	}
	if workerID != "worker-1" {
		t.Fatalf("expected worker-1, got %q", workerID)
	}

	revoked, err := RevokeWorkerToken(path, "worker-1")
	if err != nil {
		t.Fatalf("RevokeWorkerToken: %v", err)
	}
	if !revoked {
		t.Fatal("expected token to be revoked")
	}
	if _, err := MatchWorkerToken(path, token); err == nil {
		t.Fatal("MatchWorkerToken accepted revoked token")
	}

	if _, err := IssueWorkerToken(path, "bad/id", false); err == nil {
		t.Fatal("IssueWorkerToken accepted invalid worker id")
	}
}

func TestAdminTokenFilePermissionsAreRepaired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.token")
	token := strings.Repeat("a", MinConfiguredTokenLength)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadAdminToken(path); err != nil {
		t.Fatalf("LoadAdminToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token file permissions still broad: %o", info.Mode().Perm())
	}
}

func TestLegacyTokenFilePermissionsAreRepaired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens")
	tokens := Tokens{
		Admin:  strings.Repeat("a", MinConfiguredTokenLength),
		Worker: strings.Repeat("b", MinConfiguredTokenLength),
	}
	content := "admin_token=" + tokens.Admin + "\nworker_token=" + tokens.Worker + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loaded, err := LoadTokens(path)
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if loaded != tokens {
		t.Fatalf("expected %+v, got %+v", tokens, loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("legacy token file permissions still broad: %o", info.Mode().Perm())
	}
}

func TestExistingTLSKeyPermissionsAreRepaired(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, []byte("test cert\n"), 0o644); err != nil {
		t.Fatalf("WriteFile cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("test key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	generated, err := EnsureServerCertificate(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("EnsureServerCertificate: %v", err)
	}
	if generated {
		t.Fatal("expected existing cert/key to be reused")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat key: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("tls key permissions still broad: %o", info.Mode().Perm())
	}
}

func TestSanitizeLogValueRemovesControlCharacters(t *testing.T) {
	got := SanitizeLogValue("hello\n\u001b[31mred", 20)
	if strings.ContainsAny(got, "\n\r\u001b") {
		t.Fatalf("sanitize left control characters in %q", got)
	}
}
