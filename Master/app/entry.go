package master

import (
	"context"
	"flag"
	"io"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
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
	addr := extractAddr(args)
	if !canAutoStart(addr) || !shouldAutoStart(err) {
		return err
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServer(false)
	}()

	if err := waitForServer(addr, serverErr); err != nil {
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

func extractAddr(args []string) string {
	fs := flag.NewFlagSet("cerberus", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", defaultAdminAddress, "")
	_ = fs.Parse(args)
	return *addr
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

func waitForServer(addr string, serverErr <-chan error) error {
	deadline := time.Now().Add(autoStartTimeout)
	for {
		select {
		case err := <-serverErr:
			if err != nil {
				return err
			}
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
}
