package amqp

func applyOptions[T any](defaults T, opts ...func(*T)) T {
	for _, opt := range opts {
		opt(&defaults)
	}

	return defaults
}
