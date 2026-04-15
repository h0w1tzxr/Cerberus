package master

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestConstantTimeTokenEqual(t *testing.T) {
	if !constantTimeTokenEqual("token-a", "token-a") {
		t.Fatal("constantTimeTokenEqual rejected matching tokens")
	}
	if constantTimeTokenEqual("token-a", "token-b") {
		t.Fatal("constantTimeTokenEqual accepted different tokens")
	}
	if constantTimeTokenEqual("", "token-b") {
		t.Fatal("constantTimeTokenEqual accepted empty token")
	}
}

func TestExtractTokenRequiresBearer(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "raw-token"))
	if token := extractToken(ctx); token != "" {
		t.Fatalf("expected raw authorization to be rejected, got %q", token)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer token-value"))
	if token := extractToken(ctx); token != "token-value" {
		t.Fatalf("expected bearer token, got %q", token)
	}
}

func TestRoleForMethodUsesExactMatches(t *testing.T) {
	role, err := roleForMethod("/cracker.CrackerAdmin/ListTasks")
	if err != nil || role != "admin" {
		t.Fatalf("expected admin role, got role=%q err=%v", role, err)
	}

	_, err = roleForMethod("/evil.CrackerAdmin/ListTasks")
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented for unknown method, got %v", err)
	}

	_, err = roleForMethod("/cracker.CrackerServiceEvil/GetTask")
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented for substring match attempt, got %v", err)
	}
}

func TestAuthFailureRateLimit(t *testing.T) {
	policy := newAuthPolicy(strings.Repeat("a", 32), "/missing-worker-tokens.json", true)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer bad-token"))
	var err error
	for i := 0; i < authFailureLimit; i++ {
		_, err = policy.authorize(ctx, "/cracker.CrackerAdmin/ListTasks")
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("attempt %d expected Unauthenticated, got %v", i, err)
		}
	}
	_, err = policy.authorize(ctx, "/cracker.CrackerAdmin/ListTasks")
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted after failures, got %v", err)
	}
}

func TestAuthFailureTrackerIsBounded(t *testing.T) {
	policy := newAuthPolicy(strings.Repeat("a", 32), "/missing-worker-tokens.json", true)
	for i := 0; i < maxTrackedAuthFailures+10; i++ {
		policy.recordFailure(fmt.Sprintf("peer-%d", i))
	}
	if got := len(policy.failures); got > maxTrackedAuthFailures {
		t.Fatalf("failure tracker grew past limit: %d", got)
	}
}

func TestRemoteAdminRequiresKnownLoopbackPeer(t *testing.T) {
	adminToken := strings.Repeat("a", 32)
	policy := newAuthPolicy(adminToken, "/missing-worker-tokens.json", false)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+adminToken))

	_, err := policy.authorize(ctx, "/cracker.CrackerAdmin/ListTasks")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected missing peer to be rejected, got %v", err)
	}

	loopback := peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50051}})
	decision, err := policy.authorize(loopback, "/cracker.CrackerAdmin/ListTasks")
	if err != nil {
		t.Fatalf("expected loopback admin to be authorized: %v", err)
	}
	if decision.role != "admin" {
		t.Fatalf("expected admin role, got %q", decision.role)
	}
}
