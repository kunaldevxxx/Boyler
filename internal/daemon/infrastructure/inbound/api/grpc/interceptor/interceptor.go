package interceptor

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

func ContextInterceptor(defaultTimeout time.Duration, methodTimeouts map[string]time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		timeout := defaultTimeout
		if methodTimeout, ok := methodTimeouts[info.FullMethod]; ok {
			timeout = methodTimeout
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handler(ctx, req)
	}
}
