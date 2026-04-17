package master

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"cracker/Common/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type clientConfig struct {
	addr          string
	token         string
	tlsCA         string
	tlsServerName string
}

type serverConfig struct {
	listenAddr       string
	publicMode       bool
	allowRemoteAdmin bool
	tlsHosts         []string
	plainMode        bool
}

type serverSecurity struct {
	tlsConfig       *tls.Config
	adminToken      string
	workerTokenPath string
	certPath        string
	adminTokenPath  string
	generatedCert   bool
	generatedAdmin  bool
	generatedWorker bool
	defaultWorkerID string
}

var errAdminTokenMissing = errors.New("admin token is required")

const defaultListenAddress = "127.0.0.1:50051"

func loadServerSecurity(cfg serverConfig) (serverSecurity, error) {
	certPath := strings.TrimSpace(os.Getenv(security.EnvTLSCert))
	keyPath := strings.TrimSpace(os.Getenv(security.EnvTLSKey))
	if (certPath == "") != (keyPath == "") {
		return serverSecurity{}, fmt.Errorf("both %s and %s must be set together", security.EnvTLSCert, security.EnvTLSKey)
	}
	if certPath == "" || keyPath == "" {
		defaultCert, defaultKey, _, err := security.DefaultCertPaths()
		if err != nil {
			return serverSecurity{}, err
		}
		if certPath == "" {
			certPath = defaultCert
		}
		if keyPath == "" {
			keyPath = defaultKey
		}
	}

	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		hosts = append(hosts, hostname)
	}
	hosts = append(hosts, cfg.tlsHosts...)
	if cfg.publicMode && certPath == defaultCertPath() && len(cfg.tlsHosts) == 0 {
		return serverSecurity{}, fmt.Errorf("%s is required when using generated TLS certs in public mode", security.EnvTLSHosts)
	}
	generatedCert, err := security.EnsureServerCertificate(certPath, keyPath, hosts)
	if err != nil {
		return serverSecurity{}, err
	}
	tlsConfig, err := security.LoadServerTLSConfig(certPath, keyPath)
	if err != nil {
		return serverSecurity{}, err
	}

	adminToken, generatedAdmin, adminTokenPath, err := loadAdminToken()
	if err != nil {
		return serverSecurity{}, err
	}
	defaultWorkerID := defaultWorkerID()
	workerTokenPath, err := security.DefaultWorkerTokenStorePath()
	if err != nil {
		return serverSecurity{}, err
	}
	generatedWorkerToken, err := ensureWorkerTokens(workerTokenPath, defaultWorkerID)
	if err != nil {
		return serverSecurity{}, err
	}
	return serverSecurity{
		tlsConfig:       tlsConfig,
		adminToken:      adminToken,
		workerTokenPath: workerTokenPath,
		certPath:        certPath,
		adminTokenPath:  adminTokenPath,
		generatedCert:   generatedCert,
		generatedAdmin:  generatedAdmin,
		generatedWorker: generatedWorkerToken,
		defaultWorkerID: defaultWorkerID,
	}, nil
}

func defaultCertPath() string {
	certPath, _, _, err := security.DefaultCertPaths()
	if err != nil {
		return ""
	}
	return certPath
}

func loadAdminToken() (string, bool, string, error) {
	adminToken := strings.TrimSpace(os.Getenv(security.EnvAdminToken))
	if adminToken != "" {
		if err := security.ValidateTokenStrength(adminToken); err != nil {
			return "", false, "", fmt.Errorf("admin token: %w", err)
		}
		return adminToken, false, "", nil
	}
	tokenPath, err := security.DefaultAdminTokenPath()
	if err != nil {
		return "", false, "", err
	}
	token, generated, err := security.LoadOrCreateAdminToken(tokenPath)
	return token, generated, tokenPath, err
}

func ensureWorkerTokens(path, defaultWorkerID string) (bool, error) {
	generatedToken, generated, err := security.LoadOrCreateWorkerTokenStore(path, defaultWorkerID)
	if err != nil {
		return false, err
	}
	if generated && generatedToken != "" {
		return true, nil
	}
	envToken := strings.TrimSpace(os.Getenv(security.EnvWorkerToken))
	if envToken == "" {
		return generated, nil
	}
	workerID := strings.TrimSpace(os.Getenv(security.EnvWorkerID))
	if workerID == "" {
		workerID = defaultWorkerID
	}
	if err := security.EnsureWorkerToken(path, workerID, envToken); err != nil {
		return false, err
	}
	return generated, nil
}

func resolveClientConfig(cfg clientConfig, requireToken bool) (clientConfig, error) {
	if cfg.tlsCA == "" {
		cfg.tlsCA = strings.TrimSpace(os.Getenv(security.EnvTLSCA))
	}
	if cfg.tlsCA == "" {
		if defaultCert, _, _, err := security.DefaultCertPaths(); err == nil && security.FileExists(defaultCert) {
			cfg.tlsCA = defaultCert
		}
	}
	if cfg.tlsServerName == "" {
		cfg.tlsServerName = strings.TrimSpace(os.Getenv(security.EnvTLSServerName))
	}

	if requireToken && cfg.token == "" {
		cfg.token = strings.TrimSpace(os.Getenv(security.EnvAdminToken))
		if cfg.token == "" {
			if tokenPath, err := security.DefaultAdminTokenPath(); err == nil {
				if token, err := security.LoadAdminToken(tokenPath); err == nil {
					cfg.token = token
				}
			}
		}
		if cfg.token == "" {
			if legacyPath, err := security.DefaultTokenPath(); err == nil {
				if tokens, err := security.LoadTokens(legacyPath); err == nil {
					cfg.token = tokens.Admin
				}
			}
		}
		if cfg.token == "" {
			return cfg, fmt.Errorf("%w; set %s or use the generated admin.token file", errAdminTokenMissing, security.EnvAdminToken)
		}
	}

	return cfg, nil
}

func defaultServerConfig() (serverConfig, error) {
	listenAddr := strings.TrimSpace(os.Getenv(security.EnvListenAddr))
	if listenAddr == "" {
		listenAddr = defaultListenAddress
	}
	return serverConfig{
		listenAddr:       listenAddr,
		publicMode:       security.PublicMode(),
		allowRemoteAdmin: security.RemoteAdminAllowed(),
		tlsHosts:         security.TLSHostsFromEnv(),
	}, nil
}

func validateServerConfig(cfg serverConfig) error {
	if cfg.listenAddr == "" {
		return errors.New("listen address is required")
	}
	if bindsNonLoopback(cfg.listenAddr) && !cfg.publicMode {
		return fmt.Errorf("%s=1 is required to bind non-loopback address %s", security.EnvPublic, cfg.listenAddr)
	}
	return nil
}

func bindsNonLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if host == "" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback()
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "worker-local"
	}
	return "worker-" + strings.TrimSpace(hostname)
}

func clientDialOptions(cfg clientConfig, requireToken bool) ([]grpc.DialOption, error) {
	resolved, err := resolveClientConfig(cfg, requireToken)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := security.LoadClientTLSConfig(resolved.tlsCA, resolved.tlsServerName)
	if err != nil {
		return nil, fmt.Errorf("tls config error: %w", err)
	}
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
	}
	if requireToken {
		options = append(options, grpc.WithPerRPCCredentials(security.TokenCredential{Token: resolved.token}))
	}
	return options, nil
}
