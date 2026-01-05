package master

import (
	"context"
	"strings"

	"cracker/Common/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type authPolicy struct {
	adminToken  string
	workerToken string
}

func newAuthPolicy(tokens security.Tokens) authPolicy {
	return authPolicy{
		adminToken:  tokens.Admin,
		workerToken: tokens.Worker,
	}
}

func (a authPolicy) authorize(ctx context.Context, fullMethod string) error {
	required := a.requiredToken(fullMethod)
	if required == "" {
		return status.Error(codes.FailedPrecondition, "auth is not configured")
	}
	token := extractToken(ctx)
	if token == "" {
		return status.Error(codes.Unauthenticated, "missing auth token")
	}
	if token != required {
		return status.Error(codes.Unauthenticated, "invalid auth token")
	}
	return nil
}

func (a authPolicy) requiredToken(fullMethod string) string {
	if strings.Contains(fullMethod, "CrackerAdmin") {
		return a.adminToken
	}
	if strings.Contains(fullMethod, "CrackerService") {
		return a.workerToken
	}
	return a.adminToken
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
			lower := strings.ToLower(value)
			if strings.HasPrefix(lower, "bearer ") {
				return strings.TrimSpace(value[len("bearer "):])
			}
			return value
		}
	}
	if values := md.Get("x-cerberus-token"); len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	return ""
}

func unaryAuthInterceptor(policy authPolicy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := policy.authorize(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func streamAuthInterceptor(policy authPolicy) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := policy.authorize(stream.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}
