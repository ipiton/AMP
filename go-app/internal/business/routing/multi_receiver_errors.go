package routing

import "errors"

// Multi-receiver errors
var (
	// ErrAllReceiversFailed indicates all receivers failed.
	//
	// This is returned when PublishMulti() is called and
	// all receivers fail to publish.
	//
	// The MultiReceiverResult is still returned with failure details.
	ErrAllReceiversFailed = errors.New("all receivers failed")

	// ErrNoReceivers indicates no receivers found.
	//
	// This is returned when route evaluation succeeds but
	// returns no matching receivers (should be very rare,
	// as root always has a receiver).
	ErrNoReceivers = errors.New("no receivers found")
)
