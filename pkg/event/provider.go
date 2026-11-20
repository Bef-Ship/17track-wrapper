package event

import (
	"context"
	"errors"
)

// Handler consumes one envelope. Return an error to nack and retry (provider-defined).
// Wrap ErrPermanent to fail without retrying (e.g. undecodable payload).
type Handler func(ctx context.Context, env *Envelope) error

// ErrPermanent marks a handler failure that retrying cannot fix.
var ErrPermanent = errors.New("event: permanent failure")

// Provider is the only MQ plug-in point.
// V1: pkg/event/asynqprovider  V2: PulsarProvider (same interface, zero envelope change).
type Provider interface {
	Publish(ctx context.Context, channel string, scope Scope, rawEvent any) error
	Subscribe(ctx context.Context, channel string, handler Handler) error
	Close() error
}
