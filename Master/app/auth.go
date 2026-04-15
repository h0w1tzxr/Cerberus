package master

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"strings"
	"sync"
	"time"

	"cracker/Common/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type contextKey string

const (
	workerIDContextKey     contextKey = "worker_id"
	authFailureLimit                  = 20
	authFailureWindow                 = time.Minute
	maxTrackedAuthFailures            = 4096
)

type authPolicy struct {
	adminToken       string
	workerTokenPath  string
	allowRemoteAdmin bool
	mu               sync.Mutex
	failures         map[string]authFailure
}

type authFailure struct {
	count      int
	windowEnds time.Time
}

type authDecision struct {
	ctx      context.Context
	role     string
	workerID string
}

func newAuthPolicy(adminToken, workerTokenPath string, allowRemoteAdmin bool) *authPolicy {
	return &authPolicy{
		adminToken:       adminToken,
		workerTokenPath:  workerTokenPath,
		allowRemoteAdmin: allowRemoteAdmin,
		failures:         make(map[string]authFailure),
	}
}

func (a *authPolicy) authorize(ctx context.Context, fullMethod string) (authDecision, error) {
	decision := authDecision{ctx: ctx}
	peerKey := peerHost(ctx)
	if a.blocked(peerKey) {
		return decision, status.Error(codes.ResourceExhausted, "too many authentication failures")
	}
	role, err := roleForMethod(fullMethod)
	if err != nil {
		a.recordFailure(peerKey)
		return decision, err
	}
	token := extractToken(ctx)
	if token == "" {
		a.recordFailure(peerKey)
		return decision, status.Error(codes.Unauthenticated, "missing bearer auth token")
	}
	switch role {
	case "admin":
		if !a.allowRemoteAdmin && !isLoopbackPeer(ctx) {
			a.recordFailure(peerKey)
			return decision, status.Error(codes.PermissionDenied, "remote admin RPCs are disabled")
		}
		if !constantTimeTokenEqual(token, a.adminToken) {
			a.recordFailure(peerKey)
			return decision, status.Error(codes.Unauthenticated, "invalid auth token")
		}
	case "worker":
		workerID, err := security.MatchWorkerToken(a.workerTokenPath, token)
		if err != nil {
			a.recordFailure(peerKey)
			return decision, status.Error(codes.Unauthenticated, "invalid worker token")
		}
		decision.workerID = workerID
		decision.ctx = context.WithValue(ctx, workerIDContextKey, workerID)
	default:
		a.recordFailure(peerKey)
		return decision, status.Error(codes.PermissionDenied, "unsupported auth role")
	}
	decision.role = role
	a.clearFailures(peerKey)
	return decision, nil
}

func roleForMethod(fullMethod string) (string, error) {
	switch fullMethod {
	case "/cracker.CrackerAdmin/AddTask",
		"/cracker.CrackerAdmin/AddTaskBatch",
		"/cracker.CrackerAdmin/ApplyTaskAction",
		"/cracker.CrackerAdmin/GetTask",
		"/cracker.CrackerAdmin/ListTasks",
		"/cracker.CrackerAdmin/SetDispatchPaused",
		"/cracker.CrackerAdmin/ListWorkers":
		return "admin", nil
	case "/cracker.CrackerService/RegisterWorker",
		"/cracker.CrackerService/GetTask",
		"/cracker.CrackerService/ReportProgress",
		"/cracker.CrackerService/ReportResult":
		return "worker", nil
	default:
		return "", status.Error(codes.Unimplemented, "unknown gRPC method")
	}
}

func constantTimeTokenEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

func extractToken(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if values := md.Get("authorization"); len(values) > 0 {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			parts := strings.SplitN(value, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func workerIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(workerIDContextKey).(string)
	return value
}

func peerHost(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}

func isLoopbackPeer(ctx context.Context) bool {
	host := peerHost(ctx)
	if host == "unknown" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *authPolicy) blocked(peerKey string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	failure, ok := a.failures[peerKey]
	if !ok {
		return false
	}
	if time.Now().After(failure.windowEnds) {
		delete(a.failures, peerKey)
		return false
	}
	return failure.count >= authFailureLimit
}

func (a *authPolicy) recordFailure(peerKey string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	failure, knownPeer := a.failures[peerKey]
	if failure.windowEnds.IsZero() || now.After(failure.windowEnds) {
		failure = authFailure{windowEnds: now.Add(authFailureWindow)}
	}
	if !knownPeer && len(a.failures) >= maxTrackedAuthFailures {
		a.pruneFailuresLocked(now)
	}
	if !knownPeer && len(a.failures) >= maxTrackedAuthFailures {
		for key := range a.failures {
			delete(a.failures, key)
			break
		}
	}
	failure.count++
	a.failures[peerKey] = failure
}

func (a *authPolicy) clearFailures(peerKey string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, peerKey)
}

func (a *authPolicy) pruneFailuresLocked(now time.Time) {
	for key, failure := range a.failures {
		if now.After(failure.windowEnds) {
			delete(a.failures, key)
		}
	}
}

func unaryAuthInterceptor(policy *authPolicy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		decision, err := policy.authorize(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(decision.ctx, req)
	}
}

func streamAuthInterceptor(policy *authPolicy) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if _, err := policy.authorize(stream.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}
