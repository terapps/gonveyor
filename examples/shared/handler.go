package shared

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/terapps/gonveyor/store"
	"github.com/terapps/gonveyor/transport"
)

// DebugHandler returns a HandlerFunc that logs every field of the incoming task.
func DebugHandler() transport.HandlerFunc {
	return func(_ context.Context, task store.Task) (any, error) {
		attrs := payloadAttrs(task.Payload)

		slog.Info("task received", append([]any{"id", task.ID, "key", task.Key}, attrs...)...)

		time.Sleep(5 * time.Second)

		slog.Info("task done", append([]any{"id", task.ID, "key", task.Key}, attrs...)...)

		return nil, nil
	}
}

func payloadAttrs(payload []byte) []any {
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return []any{"payload", string(payload)}
	}

	attrs := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}

	return attrs
}
