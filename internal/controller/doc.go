// Package controller coordinates discovery, media serving, and renderer
// transports behind a serialized playback state machine.
//
// Controller is safe for concurrent use. Commands are accepted in actor order;
// Mutation.ExpectedRevision provides optional optimistic concurrency. If a
// command context is canceled before actor acceptance, the command does not
// run. After acceptance, the command returns its single final outcome and
// cancellation is propagated to adapter I/O. Cancellation never rolls back a
// committed mutation. Cleanup uses bounded controller-owned contexts so it can
// finish after caller cancellation.
//
// Snapshots and results are the stable client-facing data model. Their existing
// JSON field names are preserved for backward compatibility. MediaRef and
// SubtitleRef are in-process capabilities because they contain source openers;
// transports and servers should adapt those capabilities to external formats
// instead of exposing them directly.
//
// Callers that need substitution should declare narrow consumer-side
// interfaces containing only the Controller methods they use. Configuration
// adapters are already expressed as minimal capability interfaces.
//
// New starts owned goroutines immediately. Close is idempotent and waits for
// them; Shutdown provides a bounded wait. Canceling Config.ParentContext starts
// the same shutdown. Controller owns transports returned by TransportFactory,
// but configuration adapters remain caller-owned.
package controller
