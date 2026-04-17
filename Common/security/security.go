package security

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	EnvTLSCert          = "CERBERUS_TLS_CERT"
	EnvTLSKey           = "CERBERUS_TLS_KEY"
	EnvTLSCA            = "CERBERUS_TLS_CA"
	EnvTLSServerName    = "CERBERUS_TLS_SERVER_NAME"
	EnvAdminToken       = "CERBERUS_ADMIN_TOKEN"
	EnvWorkerToken      = "CERBERUS_WORKER_TOKEN"
	EnvWorkerID         = "CERBERUS_WORKER_ID"
	EnvMasterAddr       = "CERBERUS_MASTER_ADDR"
	EnvListenAddr       = "CERBERUS_LISTEN_ADDR"
	EnvPublic           = "CERBERUS_PUBLIC"
	EnvAdminRemote      = "CERBERUS_ADMIN_REMOTE"
	EnvTLSHosts         = "CERBERUS_TLS_HOSTS"
	EnvRevealPasswords  = "CERBERUS_REVEAL_PASSWORDS"
	EnvDataDir          = "CERBERUS_DATA_DIR"
	EnvAllowUnsafePaths = "CERBERUS_ALLOW_UNSAFE_PATHS"
)

const (
	MinConfiguredTokenLength = 32
	MaxWorkerIDLength        = 128
)

type Tokens struct {
	Admin  string
	Worker string
}

type WorkerTokenRecord struct {
	WorkerID      string `json:"worker_id"`
	TokenSHA256   string `json:"token_sha256"`
	CreatedAtUnix int64  `json:"created_at_unix"`
	RevokedAtUnix int64  `json:"revoked_at_unix,omitempty"`
}

type workerTokenFile struct {
	Tokens []WorkerTokenRecord `json:"tokens"`
}

type TokenCredential struct {
	Token string
}

func (t TokenCredential) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	if t.Token == "" {
		return nil, errors.New("token is empty")
	}
	return map[string]string{
		"authorization": "Bearer " + t.Token,
	}, nil
}

func (t TokenCredential) RequireTransportSecurity() bool {
	return true
}

func ConfigDir() (string, error) {
	baseDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "cerberus"), nil
}

func EnsureConfigDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func DataDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(EnvDataDir)); dir != "" {
		return filepath.Abs(dir)
	}
	configDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "data"), nil
}

func EnsureDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

func DefaultCertPaths() (string, string, string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", "", "", err
	}
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	return certPath, keyPath, certPath, nil
}

func DefaultTokenPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tokens"), nil
}

func DefaultAdminTokenPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "admin.token"), nil
}

func DefaultWorkerTokenStorePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "worker_tokens.json"), nil
}

func DefaultEvictedWorkersPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "evicted_workers.txt"), nil
}

func DefaultWorkerTokenSecretPath(workerID string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	fileName, err := workerTokenSecretFileName(workerID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workers", fileName+".token"), nil
}

func workerTokenSecretFileName(workerID string) (string, error) {
	return ValidateWorkerID(workerID)
}

func ValidateWorkerID(workerID string) (string, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || workerID == "." || workerID == ".." {
		return "", errors.New("worker id is required")
	}
	if len(workerID) > MaxWorkerIDLength {
		return "", fmt.Errorf("worker id must be at most %d characters", MaxWorkerIDLength)
	}
	for _, ch := range workerID {
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= 'A' && ch <= 'Z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return "", fmt.Errorf("worker id contains unsupported character %q", ch)
	}
	return workerID, nil
}

func FileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func RevealPasswords() bool {
	return truthyEnv(EnvRevealPasswords)
}

func AllowUnsafePaths() bool {
	return truthyEnv(EnvAllowUnsafePaths)
}

func truthyEnv(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes"
}

func PublicMode() bool {
	return truthyEnv(EnvPublic)
}

func RemoteAdminAllowed() bool {
	return truthyEnv(EnvAdminRemote)
}

func TLSHostsFromEnv() []string {
	return splitCSVEnv(EnvTLSHosts)
}

func splitCSVEnv(name string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		values = append(values, part)
	}
	return values
}

func SanitizeLogValue(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	count := 0
	for _, r := range value {
		if maxRunes > 0 && count >= maxRunes {
			break
		}
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			builder.WriteByte(' ')
			count++
		case r < 32 || r == 127:
			continue
		default:
			builder.WriteRune(r)
			count++
		}
	}
	return strings.TrimSpace(builder.String())
}

func ResolveWordlistPath(path string) (string, error) {
	return resolveDataPath(path, true)
}

func ResolveOutputPath(path string) (string, error) {
	return resolveDataPath(path, false)
}

func DataPathReference(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if AllowUnsafePaths() {
		return path, nil
	}
	root, err := EnsureDataDir()
	if err != nil {
		return "", err
	}
	absolute, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	if err := ensurePathWithin(root, absolute); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, absolute)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", errors.New("data path cannot reference data dir root")
	}
	return filepath.ToSlash(rel), nil
}

func PrepareOutputPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	cleanPath, err := absoluteCleanPath(path)
	if err != nil {
		return err
	}
	if !AllowUnsafePaths() {
		resolved, err := ResolveOutputPath(cleanPath)
		if err != nil {
			return err
		}
		if resolved != cleanPath {
			return fmt.Errorf("output path %s does not match resolved safe path %s", cleanPath, resolved)
		}
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return err
	}
	if AllowUnsafePaths() {
		return rejectDirectory(cleanPath, "output")
	}
	root, err := EnsureDataDir()
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(cleanPath))
	if err != nil {
		return err
	}
	if err := ensurePathWithin(root, parent); err != nil {
		return err
	}
	if err := rejectDirectory(cleanPath, "output"); err != nil {
		return err
	}
	if evaluated, err := filepath.EvalSymlinks(cleanPath); err == nil {
		return ensurePathWithin(root, evaluated)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func resolveDataPath(path string, mustExist bool) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if AllowUnsafePaths() {
		cleanPath, err := absoluteCleanPath(path)
		if err != nil {
			return "", err
		}
		if mustExist {
			if err := rejectMissingOrDirectory(cleanPath, "wordlist"); err != nil {
				return "", err
			}
		}
		return cleanPath, nil
	}

	root, err := EnsureDataDir()
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(path)
	var candidate string
	if filepath.IsAbs(cleaned) {
		candidate = cleaned
	} else {
		candidate = filepath.Join(root, cleaned)
	}
	candidate, err = absoluteCleanPath(candidate)
	if err != nil {
		return "", err
	}
	if err := ensurePathWithin(root, candidate); err != nil {
		return "", err
	}
	if mustExist {
		if err := rejectMissingOrDirectory(candidate, "wordlist"); err != nil {
			return "", err
		}
		evaluated, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", err
		}
		if err := ensurePathWithin(root, evaluated); err != nil {
			return "", err
		}
		return evaluated, nil
	}
	ancestor, err := nearestExistingAncestor(candidate)
	if err != nil {
		return "", err
	}
	evaluatedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	if err := ensurePathWithin(root, evaluatedAncestor); err != nil {
		return "", err
	}
	if err := rejectDirectory(candidate, "output"); err != nil {
		return "", err
	}
	if evaluated, err := filepath.EvalSymlinks(candidate); err == nil {
		if err := ensurePathWithin(root, evaluated); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return candidate, nil
}

func absoluteCleanPath(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func ensurePathWithin(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return nil
	}
	return fmt.Errorf("path %s escapes Cerberus data dir %s", path, root)
}

func nearestExistingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				current = filepath.Dir(current)
				continue
			}
			return current, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		current = parent
	}
}

func rejectMissingOrDirectory(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s %s is a directory", label, path)
	}
	return nil
}

func rejectDirectory(path, label string) error {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return fmt.Errorf("%s %s is a directory", label, path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ValidateTokens(tokens Tokens) error {
	if tokens.Admin == "" || tokens.Worker == "" {
		return errors.New("both admin and worker tokens are required")
	}
	if tokens.Admin == tokens.Worker {
		return errors.New("admin and worker tokens must be different")
	}
	if err := ValidateTokenStrength(tokens.Admin); err != nil {
		return fmt.Errorf("admin token: %w", err)
	}
	if err := ValidateTokenStrength(tokens.Worker); err != nil {
		return fmt.Errorf("worker token: %w", err)
	}
	return nil
}

func ValidateTokenStrength(token string) error {
	if len(strings.TrimSpace(token)) < MinConfiguredTokenLength {
		return fmt.Errorf("must be at least %d characters", MinConfiguredTokenLength)
	}
	return nil
}

func EnsureServerCertificate(certPath, keyPath string, hosts []string) (bool, error) {
	certExists := FileExists(certPath)
	keyExists := FileExists(keyPath)
	if certExists && keyExists {
		if err := ensurePrivateFileMode(keyPath, 0o600); err != nil {
			return false, err
		}
		return false, nil
	}
	if certExists != keyExists {
		return false, fmt.Errorf("tls cert/key mismatch (cert=%t key=%t)", certExists, keyExists)
	}

	if certPath == "" || keyPath == "" {
		return false, errors.New("tls cert/key path is required")
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return false, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, err
	}
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Cerberus"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	if len(template.DNSNames) == 0 && len(template.IPAddresses) == 0 {
		template.DNSNames = append(template.DNSNames, "localhost")
		template.IPAddresses = append(template.IPAddresses, net.ParseIP("127.0.0.1"))
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return false, err
	}

	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return false, err
	}

	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return false, err
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}); err != nil {
		return false, err
	}

	return true, nil
}

func LoadServerTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	if certPath == "" || keyPath == "" {
		return nil, errors.New("tls cert/key path is required")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func LoadClientTLSConfig(caPath, serverName string) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caPath != "" {
		caBytes, err := os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
		if ok := pool.AppendCertsFromPEM(caBytes); !ok {
			return nil, errors.New("failed to parse CA certificate")
		}
	}
	config := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	if serverName != "" {
		config.ServerName = serverName
	}
	return config, nil
}

func LoadOrCreateAdminToken(path string) (string, bool, error) {
	if path == "" {
		return "", false, errors.New("admin token path is required")
	}
	if FileExists(path) {
		token, err := LoadAdminToken(path)
		return token, false, err
	}
	token, err := GenerateToken()
	if err != nil {
		return "", false, err
	}
	if err := SaveAdminToken(path, token); err != nil {
		return "", false, err
	}
	return token, true, nil
}

func LoadAdminToken(path string) (string, error) {
	token, err := loadTokenSecret(path)
	if err != nil {
		return "", err
	}
	if err := ValidateTokenStrength(token); err != nil {
		return "", err
	}
	return token, nil
}

func SaveAdminToken(path, token string) error {
	if err := ValidateTokenStrength(token); err != nil {
		return err
	}
	return saveTokenSecret(path, token)
}

func LoadWorkerTokenSecret(workerID string) (string, error) {
	path, err := DefaultWorkerTokenSecretPath(workerID)
	if err != nil {
		return "", err
	}
	token, err := loadTokenSecret(path)
	if err != nil {
		return "", err
	}
	if err := ValidateTokenStrength(token); err != nil {
		return "", err
	}
	return token, nil
}

func SaveWorkerTokenSecret(workerID, token string) error {
	path, err := DefaultWorkerTokenSecretPath(workerID)
	if err != nil {
		return err
	}
	if err := ValidateTokenStrength(token); err != nil {
		return err
	}
	return saveTokenSecret(path, token)
}

func loadTokenSecret(path string) (string, error) {
	if path == "" {
		return "", errors.New("token path is required")
	}
	if err := ensurePrivateFileMode(path, 0o600); err != nil {
		return "", err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(bytes))
	if token == "" {
		return "", errors.New("token file is empty")
	}
	return token, nil
}

func saveTokenSecret(path, token string) error {
	if path == "" {
		return errors.New("token path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return err
	}
	return ensurePrivateFileMode(path, 0o600)
}

func LoadOrCreateWorkerTokenStore(path, defaultWorkerID string) (string, bool, error) {
	defaultWorkerID, err := ValidateWorkerID(defaultWorkerID)
	if err != nil {
		return "", false, err
	}
	store, err := loadWorkerTokenFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", false, err
		}
		store = workerTokenFile{}
	}
	for _, record := range store.Tokens {
		if record.WorkerID == defaultWorkerID && record.RevokedAtUnix == 0 {
			return "", false, nil
		}
	}
	token, err := IssueWorkerToken(path, defaultWorkerID, true)
	if err != nil {
		return "", false, err
	}
	return token, true, nil
}

func IssueWorkerToken(path, workerID string, writeSecret bool) (string, error) {
	var err error
	workerID, err = ValidateWorkerID(workerID)
	if err != nil {
		return "", err
	}
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	if err := EnsureWorkerToken(path, workerID, token); err != nil {
		return "", err
	}
	if writeSecret {
		if err := SaveWorkerTokenSecret(workerID, token); err != nil {
			return "", err
		}
	}
	return token, nil
}

func EnsureWorkerToken(path, workerID, token string) error {
	if path == "" {
		return errors.New("worker token store path is required")
	}
	workerID, err := ValidateWorkerID(workerID)
	if err != nil {
		return err
	}
	if err := ValidateTokenStrength(token); err != nil {
		return err
	}
	store, err := loadWorkerTokenFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		store = workerTokenFile{}
	}
	now := time.Now().Unix()
	tokenHash := TokenSHA256(token)
	found := false
	for i := range store.Tokens {
		if store.Tokens[i].WorkerID != workerID {
			continue
		}
		if store.Tokens[i].RevokedAtUnix == 0 {
			store.Tokens[i].RevokedAtUnix = now
		}
		if store.Tokens[i].TokenSHA256 == tokenHash {
			store.Tokens[i].RevokedAtUnix = 0
			found = true
		}
	}
	if !found {
		store.Tokens = append(store.Tokens, WorkerTokenRecord{
			WorkerID:      workerID,
			TokenSHA256:   tokenHash,
			CreatedAtUnix: now,
		})
	}
	return saveWorkerTokenFile(path, store)
}

func RevokeWorkerToken(path, workerID string) (bool, error) {
	workerID, err := ValidateWorkerID(workerID)
	if err != nil {
		return false, err
	}
	store, err := loadWorkerTokenFile(path)
	if err != nil {
		return false, err
	}
	revoked := false
	now := time.Now().Unix()
	for i := range store.Tokens {
		if store.Tokens[i].WorkerID == workerID && store.Tokens[i].RevokedAtUnix == 0 {
			store.Tokens[i].RevokedAtUnix = now
			revoked = true
		}
	}
	if !revoked {
		return false, nil
	}
	return true, saveWorkerTokenFile(path, store)
}

func ListWorkerTokens(path string) ([]WorkerTokenRecord, error) {
	store, err := loadWorkerTokenFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	records := append([]WorkerTokenRecord(nil), store.Tokens...)
	sort.Slice(records, func(i, j int) bool {
		if records[i].WorkerID != records[j].WorkerID {
			return records[i].WorkerID < records[j].WorkerID
		}
		return records[i].CreatedAtUnix < records[j].CreatedAtUnix
	})
	return records, nil
}

func MatchWorkerToken(path, token string) (string, error) {
	if err := ValidateTokenStrength(token); err != nil {
		return "", err
	}
	store, err := loadWorkerTokenFile(path)
	if err != nil {
		return "", err
	}
	tokenHash := TokenSHA256(token)
	var matchedWorker string
	for _, record := range store.Tokens {
		if record.RevokedAtUnix != 0 {
			continue
		}
		if constantTimeHexEqual(tokenHash, record.TokenSHA256) {
			matchedWorker = record.WorkerID
		}
	}
	if matchedWorker == "" {
		return "", errors.New("worker token not found")
	}
	return matchedWorker, nil
}

func TokenSHA256(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func constantTimeHexEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	gotSum := sha256.Sum256([]byte(got))
	wantSum := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotSum[:], wantSum[:]) == 1
}

func loadWorkerTokenFile(path string) (workerTokenFile, error) {
	if path == "" {
		return workerTokenFile{}, errors.New("worker token store path is required")
	}
	if err := ensurePrivateFileMode(path, 0o600); err != nil {
		return workerTokenFile{}, err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return workerTokenFile{}, err
	}
	if len(strings.TrimSpace(string(bytes))) == 0 {
		return workerTokenFile{}, nil
	}
	var store workerTokenFile
	if err := json.Unmarshal(bytes, &store); err != nil {
		return workerTokenFile{}, err
	}
	return store, nil
}

func saveWorkerTokenFile(path string, store workerTokenFile) error {
	if path == "" {
		return errors.New("worker token store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return err
	}
	return ensurePrivateFileMode(path, 0o600)
}

func ensurePrivateFileMode(path string, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	info, err = os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s permissions %o are too broad", path, info.Mode().Perm())
	}
	return nil
}

func LoadTokens(path string) (Tokens, error) {
	if path == "" {
		return Tokens{}, errors.New("token path is required")
	}
	if err := ensurePrivateFileMode(path, 0o600); err != nil {
		return Tokens{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Tokens{}, err
	}
	defer file.Close()

	var tokens Tokens
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "admin_token", "admin":
			tokens.Admin = value
		case "worker_token", "worker":
			tokens.Worker = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Tokens{}, err
	}
	if tokens.Admin == "" || tokens.Worker == "" {
		return Tokens{}, errors.New("tokens file missing admin or worker token")
	}
	return tokens, ValidateTokens(tokens)
}

func LoadOrCreateTokens(path string) (Tokens, bool, error) {
	if FileExists(path) {
		tokens, err := LoadTokens(path)
		return tokens, false, err
	}
	adminToken, err := GenerateToken()
	if err != nil {
		return Tokens{}, false, err
	}
	workerToken, err := GenerateToken()
	if err != nil {
		return Tokens{}, false, err
	}
	tokens := Tokens{Admin: adminToken, Worker: workerToken}
	if err := ValidateTokens(tokens); err != nil {
		return Tokens{}, false, err
	}
	if err := SaveTokens(path, tokens); err != nil {
		return Tokens{}, false, err
	}
	return tokens, true, nil
}

func SaveTokens(path string, tokens Tokens) error {
	if path == "" {
		return errors.New("token path is required")
	}
	if tokens.Admin == "" || tokens.Worker == "" {
		return errors.New("both admin and worker tokens are required")
	}
	if err := ValidateTokens(tokens); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf("admin_token=%s\nworker_token=%s\n", tokens.Admin, tokens.Worker)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	return ensurePrivateFileMode(path, 0o600)
}

func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
