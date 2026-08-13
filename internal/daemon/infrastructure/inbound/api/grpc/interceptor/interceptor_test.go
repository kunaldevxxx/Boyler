package interceptor

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestContextInterceptorUsesDefaultAndMethodTimeouts(t *testing.T) {
	const longMethod = "/daemon.ImageService/PruneImages"
	interceptor := ContextInterceptor(20*time.Second, map[string]time.Duration{
		longMethod: 2 * time.Minute,
	})

	for _, test := range []struct {
		name       string
		method     string
		wantWindow time.Duration
	}{
		{name: "default", method: "/daemon.ContainerService/Ps", wantWindow: 20 * time.Second},
		{name: "override", method: longMethod, wantWindow: 2 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: test.method}, func(ctx context.Context, _ any) (any, error) {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("interceptor did not set a deadline")
				}
				remaining := time.Until(deadline)
				if remaining < test.wantWindow-time.Second || remaining > test.wantWindow {
					t.Fatalf("deadline remaining = %s, want approximately %s", remaining, test.wantWindow)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
