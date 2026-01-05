package master

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"cracker/Common/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"
)

const autoStartTimeout = 5 * time.Second

func Run(args []string) error {
	if len(args) == 0 {
		return runServer(true)
	}

	err := handleCLI(args)
	if err == nil {
		return nil
	}
	cfg := extractClientConfig(args)
	missingToken := errors.Is(err, errAdminTokenMissing)
	if !canAutoStart(cfg.addr) || (!shouldAutoStart(err) && !missingToken) {
		return err
	}
	if missingToken {
		if tokenPath, pathErr := security.DefaultTokenPath(); pathErr == nil && security.FileExists(tokenPath) {
			return err
		}
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServer(false)
	}()

	if err := waitForServer(cfg, serverErr); err != nil {
		return err
	}
	if err := handleCLI(args); err != nil {
		return err
	}
	return <-serverErr
}

func shouldAutoStart(err error) bool {
	if err == nil {
		return false
	}
	statusInfo, ok := status.FromError(err)
	if ok && statusInfo.Code() == codes.Unavailable {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "connection refused") || strings.Contains(lower, "connection error")
}

func extractClientConfig(args []string) clientConfig {
	cfg, _, _, err := parseGlobalFlags(args)
	if err != nil {
		return clientConfig{addr: defaultAdminAddress}
	}
	if cfg.addr == "" {
		cfg.addr = defaultAdminAddress
	}
	return cfg
}

func canAutoStart(addr string) bool {
	if addr == "" {
		return false
	}
	if addr == defaultAdminAddress {
		return true
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if port != strings.TrimPrefix(Port, ":") {
		return false
	}
	return host == "" || host == "localhost" || host == "127.0.0.1"
}

func waitForServer(cfg clientConfig, serverErr <-chan error) error {
	deadline := time.Now().Add(autoStartTimeout)
	var lastErr error
	for {
		select {
		case err := <-serverErr:
			if err != nil {
				return err
			}
		default:
		}

		options, err := clientDialOptions(cfg, false)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		conn, err := grpc.NewClient(cfg.addr, options...)
		if err == nil {
			err = waitForReady(ctx, conn)
			_ = conn.Close()
			if err == nil {
				cancel()
				return nil
			}
		}
		cancel()
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func waitForReady(ctx context.Context, conn *grpc.ClientConn) error {
	if conn == nil {
		return errors.New("client connection is nil")
	}
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.Shutdown {
			return errors.New("connection shutdown")
		}
		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}
