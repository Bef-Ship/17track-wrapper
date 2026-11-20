package event

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope is the stable outer shell. rawEvent is the native payload, unmodified.
type Envelope struct {
	Channel   string          `json:"channel"`
	Scope     Scope           `json:"scope"`
	EventID   string          `json:"eventId"`
	Timestamp string          `json:"timestamp"`
	RawEvent  json.RawMessage `json:"rawEvent"`
}

type dedupeKeyCtx struct{}

// WithDedupeKey makes the next Publish on ctx idempotent for key: providers drop a
// repeat publish of the same channel+key within their dedupe window. Without a
// key every publish is delivered, even when payloads are identical.
func WithDedupeKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, dedupeKeyCtx{}, key)
}

// DedupeKey returns the key set by WithDedupeKey, or "".
func DedupeKey(ctx context.Context) string {
	k, _ := ctx.Value(dedupeKeyCtx{}).(string)
	return k
}

// WrapContext is Wrap with the event id derived from ctx's dedupe key when set.
func WrapContext(ctx context.Context, channel string, scope Scope, rawEvent any) (*Envelope, error) {
	env, err := Wrap(channel, scope, rawEvent)
	if err != nil {
		return nil, err
	}
	if k := DedupeKey(ctx); k != "" {
		env.EventID = EventID(channel, json.RawMessage(k))
	}
	return env, nil
}

// Wrap builds an envelope with a unique event id.
func Wrap(channel string, scope Scope, rawEvent any) (*Envelope, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if err := ValidateChannel(channel, scope.SourceType); err != nil {
		return nil, err
	}
	raw, err := MarshalRaw(rawEvent)
	if err != nil {
		return nil, err
	}
	return &Envelope{
		Channel:   channel,
		Scope:     scope,
		EventID:   uuid.NewString(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RawEvent:  raw,
	}, nil
}

// MarshalRaw keeps JSON objects as objects. Non-JSON (e.g. WeCom XML) is stored as a JSON string.
func MarshalRaw(rawEvent any) (json.RawMessage, error) {
	switch v := rawEvent.(type) {
	case json.RawMessage:
		return normalizeRawBytes(v)
	case []byte:
		return normalizeRawBytes(v)
	case string:
		return normalizeRawBytes([]byte(v))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("event rawEvent: %w", err)
		}
		return b, nil
	}
}

func normalizeRawBytes(b []byte) (json.RawMessage, error) {
	if json.Valid(b) {
		return json.RawMessage(b), nil
	}
	enc, err := json.Marshal(string(b))
	if err != nil {
		return nil, err
	}
	return enc, nil
}

// EventID hashes channel and key into a stable id for deduplicated publishes.
func EventID(channel string, raw json.RawMessage) string {
	h := sha256.New()
	_, _ = h.Write([]byte(channel))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}
