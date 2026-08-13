package grpc

import (
	"context"
	"fmt"

	grpc "google.golang.org/grpc"

	application "boyler/internal/daemon/application/container_service"
	imageservice "boyler/internal/daemon/application/image_service"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
)

type DaemonHandler struct {
	containerService application.ContainerService
	pb.UnimplementedContainerServiceServer
	imageService imageservice.ImageService
	pb.UnimplementedImageServiceServer
}

func NewDaemonHandler(containerService application.ContainerService, imageService imageservice.ImageService) *DaemonHandler {
	return &DaemonHandler{containerService: containerService, imageService: imageService}
}

func (d *DaemonHandler) CreateContainer(ctx context.Context, req *pb.CreateRequest) (*pb.CreateResponse, error) {
	command := MapCreateRequestToCommand(req)
	serviceResponse, err := d.containerService.CreateAndStart(ctx, command)
	if err != nil {
		return &pb.CreateResponse{}, err
	}
	return MapCreateResponceToProto(serviceResponse), nil
}

func (d *DaemonHandler) StartContainer(ctx context.Context, req *pb.StartRequest) (*pb.StartResponse, error) {
	command := MapStartRequestToCommand(req)
	serviceResponse, err := d.containerService.Start(ctx, command)
	if err != nil {
		return &pb.StartResponse{}, err
	}
	return MapStartResponceToProto(serviceResponse), nil
}

func (d *DaemonHandler) StopContainer(ctx context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	command := MapStopRequestToCommand(req)
	serviceResponse, err := d.containerService.Stop(ctx, command)
	if err != nil {
		return &pb.StopResponse{}, err
	}
	return MapStopResponseToProto(serviceResponse), nil
}

func (d *DaemonHandler) RemoveContainer(ctx context.Context, req *pb.RemoveRequest) (*pb.RemoveResponse, error) {
	command := MapRemoveRequestToCommand(req)
	serviveResponse, err := d.containerService.Remove(ctx, command)
	if err != nil {
		return &pb.RemoveResponse{}, err
	}
	return MapRemoveResponseToProto(serviveResponse), nil
}

func (d *DaemonHandler) InspectContainer(ctx context.Context, req *pb.InspectRequest) (*pb.InspectResponse, error) {
	command := MapInsRequestToCommand(req)
	serviceResponse, err := d.containerService.Inspect(ctx, command)
	if err != nil {
		return &pb.InspectResponse{}, err
	}
	return MapInspectResponseToProto(serviceResponse), nil
}

func (d *DaemonHandler) AttachContainer(req grpc.BidiStreamingServer[pb.AttachRequest, pb.AttachResponse]) error {
	return d.containerService.Attach(req.Context(), &grpcStream{stream: req})
}

func (d *DaemonHandler) ContainersList(ctx context.Context, req *pb.PsRequest) (*pb.PsResponse, error) {
	command := application.PsCommand{}
	serviceResponse, err := d.containerService.PsInspect(ctx, command)
	if err != nil {
		return &pb.PsResponse{}, err
	}
	containers := MapPsResponseToProto(serviceResponse)
	return &pb.PsResponse{
		Containers: containers,
	}, nil
}

func (d *DaemonHandler) PullImage(req *pb.PullImageRequest, stream pb.ImageService_PullImageServer) error {
	ctx := stream.Context()
	return d.imageService.Pull(ctx, req.GetImageIdentity(), &grpcProgressStream{stream: stream})
}

func (d *DaemonHandler) RemoveImage(ctx context.Context, req *pb.RemoveImageRequest) (*pb.RemoveImageResponse, error) {
	references := req.GetImageReferences()
	if len(references) == 0 && req.GetImageId() != "" {
		references = []string{req.GetImageId()}
	}
	if len(references) == 0 {
		return nil, fmt.Errorf("at least one image reference is required")
	}
	response := &pb.RemoveImageResponse{Status: "removed"}
	seen := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if _, ok := seen[reference]; ok {
			continue
		}
		seen[reference] = struct{}{}
		result, err := d.imageService.Remove(ctx, imageservice.RemoveCommand{ImageIdentify: reference, Force: req.GetForce()})
		if err != nil {
			response.Failures = append(response.Failures, &pb.ImageOperationError{Reference: reference, Error: err.Error()})
			continue
		}
		response.Images = append(response.Images, &pb.RemovedImage{Reference: result.Reference, Digest: result.Digest})
	}
	if len(response.Failures) > 0 {
		response.Status = "partial"
	}
	return response, nil
}

func (d *DaemonHandler) ListImages(ctx context.Context, req *pb.ListImagesRequest) (*pb.ListImagesResponse, error) {
	images, err := d.imageService.List(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.ListImagesResponse{Images: MapImagesToProto(images)}, nil
}

func (d *DaemonHandler) InspectImage(ctx context.Context, req *pb.InspectImageRequest) (*pb.ImageSummary, error) {
	image, err := d.imageService.Inspect(ctx, req.GetImageReference())
	if err != nil {
		return nil, err
	}
	return MapImageToProto(image), nil
}

func (d *DaemonHandler) PruneImages(ctx context.Context, req *pb.PruneImagesRequest) (*pb.PruneImagesResponse, error) {
	result, err := d.imageService.Prune(ctx, imageservice.PruneCommand{All: req.GetAll(), DryRun: req.GetDryRun()})
	if err != nil {
		return nil, err
	}
	return &pb.PruneImagesResponse{
		DeletedReferences:     result.DeletedReferences,
		DeletedManifests:      result.DeletedManifests,
		DeletedRootfs:         result.DeletedRootfs,
		DeletedLayers:         result.DeletedLayers,
		QuarantinedReferences: result.QuarantinedReferences,
		ReclaimedBytes:        result.ReclaimedBytes,
		DryRun:                req.GetDryRun(),
	}, nil
}
