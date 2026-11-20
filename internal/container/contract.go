package container

import "context"

type positionWorker interface {
	Start(context.Context)
	Shutdown(context.Context) error
}
