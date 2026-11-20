package memprovider

import (
	"context"
	"sync"

	"github.com/SolaTyolo/17track-wrapper/pkg/event"
)

// Provider is an in-process bus for tests and local runs without Redis.
type Provider struct {
	mu        sync.Mutex
	seen      map[string]struct{}
	handlers  map[string][]event.Handler
	Published []*event.Envelope
}

func New() *Provider {
	return &Provider{
		seen:     map[string]struct{}{},
		handlers: map[string][]event.Handler{},
	}
}

func (p *Provider) Publish(ctx context.Context, channel string, scope event.Scope, rawEvent any) error {
	env, err := event.WrapContext(ctx, channel, scope, rawEvent)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if _, dup := p.seen[env.EventID]; dup {
		p.mu.Unlock()
		return nil
	}
	p.seen[env.EventID] = struct{}{}
	p.Published = append(p.Published, env)
	hs := append([]event.Handler(nil), p.handlers[channel]...)
	p.mu.Unlock()

	for _, h := range hs {
		if err := h(ctx, env); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) Subscribe(_ context.Context, channel string, handler event.Handler) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[channel] = append(p.handlers[channel], handler)
	return nil
}

func (p *Provider) Close() error { return nil }
