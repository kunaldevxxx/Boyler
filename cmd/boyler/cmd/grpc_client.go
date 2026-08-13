package cmd

import (
	"os"
	"time"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const daemonRequestTimeout = 20 * time.Second

func NewGrpcDaemonClient() (pb.ContainerServiceClient, *grpc.ClientConn, error) {
	target := "unix://" + os.Getenv("UNIX_SOCKET")
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	client := pb.NewContainerServiceClient(connection)
	return client, connection, nil
}

func NewGrpcDaemonPullingClient() (pb.ImageServiceClient, *grpc.ClientConn, error) {
	target := "unix://" + os.Getenv("UNIX_SOCKET")
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	client := pb.NewImageServiceClient(connection)
	return client, connection, nil
}

func NewGrpcDaemonInspectionClient() (pb.DaemonInspectionServiceClient, *grpc.ClientConn, error) {
	target := "unix://" + os.Getenv("UNIX_SOCKET")
	connection, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(4<<20)),
	)
	if err != nil {
		return nil, nil, err
	}
	return pb.NewDaemonInspectionServiceClient(connection), connection, nil
}
