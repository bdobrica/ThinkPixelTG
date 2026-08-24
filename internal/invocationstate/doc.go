// Package invocationstate implements the pure, versioned invocation lifecycle.
//
// It has no persistence or adapter dependencies. Callers verify external facts
// (such as a worker fence or approval binding), pass those facts to Transition,
// and persist the returned snapshot atomically with audit and outbox records.
package invocationstate
