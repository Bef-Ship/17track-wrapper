package infrastructure

import (
	"context"
	"fmt"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/pkg/event"
)

const registerSource = "track17_register"

// RegisterQueue publishes deferred register jobs onto internal.track17_register.
type RegisterQueue struct {
	bus event.Provider
}

func NewRegisterQueue(bus event.Provider) *RegisterQueue {
	return &RegisterQueue{bus: bus}
}

func (q *RegisterQueue) EnqueueRegister(ctx context.Context, job domain.RegisterJob) error {
	if q == nil || q.bus == nil {
		return fmt.Errorf("register queue unavailable")
	}
	ch, err := event.Channel(event.SourceInternal, registerSource)
	if err != nil {
		return err
	}
	return q.bus.Publish(ctx, ch, event.InternalBiz(""), job)
}

// RegisterChannel is the asynq/event channel name for register retries.
func RegisterChannel() string {
	ch, _ := event.Channel(event.SourceInternal, registerSource)
	return ch
}
