package security

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	EnvTLSCert       = "CERBERUS_TLS_CERT"
	EnvTLSKey        = "CERBERUS_TLS_KEY"
	EnvTLSCA         = "CERBERUS_TLS_CA"
	EnvTLSServerName = "CERBERUS_TLS_SERVER_NAME"
	EnvAdminToken    = "CERBERUS_ADMIN_TOKEN"
	EnvWorkerToken   = "CERBERUS_WORKER_TOKEN"
	EnvMasterAddr    = "CERBERUS_MASTER_ADDR"
)

type Tokens struct {
	Admin  string
	Worker string
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

func FileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func EnsureServerCertificate(certPath, keyPath string, hosts []string) (bool, error) {
	certExists := FileExists(certPath)
	keyExists := FileExists(keyPath)
	if certExists && keyExists {
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

func LoadTokens(path string) (Tokens, error) {
	if path == "" {
		return Tokens{}, errors.New("token path is required")
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
	return tokens, nil
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf("admin_token=%s\nworker_token=%s\n", tokens.Admin, tokens.Worker)
	return os.WriteFile(path, []byte(content), 0o600)
}

func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
