// Package grpcsvc is the gRPC transport for example.
//
// The generated pb.go files land in this directory once `gen proto:build` has
// run. Until then this file compiles on its own: it holds the usecase and the
// mapping helpers but does not yet reference the generated types, so a module
// scaffolded with --grpc still builds before the protobuf toolchain is
// installed.
package grpcsvc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/MAbduhI/go-boilerplate/shared/httpx"

	"github.com/MAbduhI/go-boilerplate/modules/example/model"
	"github.com/MAbduhI/go-boilerplate/modules/example/usecase"
)

// Server implements ExampleServiceServer once the proto is compiled.
//
// After running `gen proto:build`, embed the generated unimplemented struct so
// the type keeps satisfying the service as new RPCs are added:
//
//	type Server struct {
//	    UnimplementedExampleServiceServer
//	    usecase usecase.ExampleUsecase
//	}
type Server struct {
	usecase usecase.ExampleUsecase
}

// NewServer returns the example gRPC server.
func NewServer(uc usecase.ExampleUsecase) *Server {
	return &Server{usecase: uc}
}

// Usecase exposes the usecase to the RPC methods you add below.
func (s *Server) Usecase() usecase.ExampleUsecase { return s.usecase }

// Register mounts this server on a grpc.Server. Uncomment the body once the
// proto has been compiled:
//
//	func Register(gs *grpc.Server, uc usecase.ExampleUsecase) {
//	    RegisterExampleServiceServer(gs, NewServer(uc))
//	}

// StatusError maps a domain error onto a gRPC status.
//
// The same sentinel errors serve both transports: httpx owns the vocabulary and
// each transport translates it, so a usecase never learns whether it is being
// called over HTTP or gRPC.
func StatusError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, httpx.ErrBadRequest), errors.Is(err, httpx.ErrValidation):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, httpx.ErrConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, httpx.ErrUnauthorized):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, httpx.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, httpx.ErrTooManyRequests):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	default:
		// Never surface an internal message to a gRPC caller.
		return status.Error(codes.Internal, "internal server error")
	}
}

// TODO: once `gen proto:build` has generated the pb.go files, implement the
// RPCs. Each one is a thin translation, exactly like the REST handler:
//
//	func (s *Server) GetExample(ctx context.Context, req *GetExampleRequest) (*GetExampleResponse, error) {
//	    out, err := s.usecase.GetByID(ctx, req.GetId())
//	    if err != nil {
//	        return nil, StatusError(err)
//	    }
//	    return &GetExampleResponse{ Example: toProto(out) }, nil
//	}
//
// and a mapper from the domain model onto the wire message:
//
//	func toProto(m *model.Example) *Example {
//	    return &Example{
//	        Id:          m.ID,
//	        Name:        m.Name,
//	        Description: m.Description,
//	        CreatedAt:   timestamppb.New(m.CreatedAt),
//	        UpdatedAt:   timestamppb.New(m.UpdatedAt),
//	    }
//	}
var _ = model.Example{}
