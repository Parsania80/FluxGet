//go:build !linux

package browserbridge

import (
	"context"
	"errors"
)

type Server struct{}

func Serve(func(context.Context, Request) Response) (*Server, error) {
	return nil, errors.New("browser integration currently requires Linux")
}
func (*Server) Close() {}
func Call(context.Context, Request) (Response, error) {
	return Response{}, errors.New("browser integration currently requires Linux")
}
func DesktopRunning() bool { return true }
