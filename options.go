package gonveyor

type options struct {
	eventPublisher EventPublisher
}

// Option configures a Gonveyor or Gonductor instance.
type Option func(*options)

// WithEventPublisher sets the event publisher used to emit node state transitions.
func WithEventPublisher(p EventPublisher) Option {
	return func(o *options) { o.eventPublisher = p }
}

func applyOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
