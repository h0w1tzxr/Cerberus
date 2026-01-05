package master

import (
	"crypto/tls"
	"errors"
	"fmt"
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

type serverSecurity struct {
	tlsConfig       *tls.Config
	tokens          security.Tokens
	certPath        string
	tokenPath       string
	generatedCert   bool
	generatedTokens bool
}

var errAdminTokenMissing = errors.New("admin token is required")

func loadServerSecurity() (serverSecurity, error) {
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
	generatedCert, err := security.EnsureServerCertificate(certPath, keyPath, hosts)
	if err != nil {
		return serverSecurity{}, err
	}
	tlsConfig, err := security.LoadServerTLSConfig(certPath, keyPath)
	if err != nil {
		return serverSecurity{}, err
	}

	tokens, generatedTokens, tokenPath, err := loadServerTokens()
	if err != nil {
		return serverSecurity{}, err
	}
	return serverSecurity{
		tlsConfig:       tlsConfig,
		tokens:          tokens,
		certPath:        certPath,
		tokenPath:       tokenPath,
		generatedCert:   generatedCert,
		generatedTokens: generatedTokens,
	}, nil
}

func loadServerTokens() (security.Tokens, bool, string, error) {
	adminToken := strings.TrimSpace(os.Getenv(security.EnvAdminToken))
	workerToken := strings.TrimSpace(os.Getenv(security.EnvWorkerToken))
	if adminToken != "" || workerToken != "" {
		if adminToken == "" || workerToken == "" {
			return security.Tokens{}, false, "", errors.New("both CERBERUS_ADMIN_TOKEN and CERBERUS_WORKER_TOKEN are required")
		}
		return security.Tokens{Admin: adminToken, Worker: workerToken}, false, "", nil
	}

	tokenPath, err := security.DefaultTokenPath()
	if err != nil {
		return security.Tokens{}, false, "", err
	}
	tokens, generated, err := security.LoadOrCreateTokens(tokenPath)
	return tokens, generated, tokenPath, err
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
			if tokenPath, err := security.DefaultTokenPath(); err == nil {
				if tokens, err := security.LoadTokens(tokenPath); err == nil {
					cfg.token = tokens.Admin
				}
			}
		}
		if cfg.token == "" {
			return cfg, fmt.Errorf("%w; set CERBERUS_ADMIN_TOKEN or use the generated tokens file", errAdminTokenMissing)
		}
	}

	return cfg, nil
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
