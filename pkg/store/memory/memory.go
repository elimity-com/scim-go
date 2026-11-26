// Package memory provides the in-memory store a server keeps its resources in, so they can be exported and restored.
//
// A server uses its own memory unless server.WithMemory gives it one. Export and Restore copy every resource with its id, links and
// meta, so a caller can persist the resources elsewhere and bring them back after a restart:
//
//	m := memory.New()
//	srv := server.New("/scim/v2", config, server.WithMemory(m), server.WithResource(users), server.WithResource(groups))
//	err := m.Restore(loaded)
//	...
//	saved := m.Export()
package memory

import "github.com/supabase-community/scim-go/internal/memory"

// Memory holds the resources of every resource type a server serves.
type Memory = memory.Memory

// Entry is one resource as a Memory holds it.
type Entry = memory.Entry

var (
	// ErrUnknownResourceType rejects an entry whose resource type has no store.
	ErrUnknownResourceType = memory.ErrUnknownResourceType
	// ErrDuplicateID rejects two entries with the same id.
	ErrDuplicateID = memory.ErrDuplicateID
	// ErrNotUnique rejects two entries that share a value of a unique attribute, per RFC 7643, Section 7.
	ErrNotUnique = memory.ErrNotUnique
	// ErrMissingTarget rejects a link to an id without entry, per RFC 7643, Section 2.3.7.
	ErrMissingTarget = memory.ErrMissingTarget
)

// New returns an empty Memory.
func New() *Memory {
	return memory.New()
}
