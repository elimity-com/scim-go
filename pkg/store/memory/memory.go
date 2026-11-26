// Package memory provides the in-memory store a server keeps its resources in by default.
//
// Give a server a Memory with server.WithMemory to export and restore its resources. Export and Restore copy every resource with its
// id, links and meta, so a caller can persist the resources elsewhere and bring them back after a restart:
//
//	m := memory.New()
//	srv := server.New("/scim/v2", config, server.WithMemory(m), server.WithResource(users), server.WithResource(groups))
//	err := m.Restore(loaded)
//	...
//	saved := m.Export()
package memory

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/supabase-community/scim-go/internal/etag"
	"github.com/supabase-community/scim-go/internal/fields"
	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/store"
)

// Memory holds the resources of every resource type a server serves.
type Memory struct {
	mu      sync.Mutex
	entries entries
	unique  uniqueIndex
	inbound inbound
	stores  map[core.ResourceTypeName]*memoryStore
}

// New returns an empty Memory.
func New() *Memory {
	return &Memory{
		entries: entries{},
		unique:  uniqueIndex{},
		inbound: inbound{},
		stores:  map[core.ResourceTypeName]*memoryStore{},
	}
}

// Store returns the store of one resource type. Restore resolves the resource type of an entry to the store most recently returned for it.
func (m *Memory) Store(location string, schemas core.Schemas) store.Store {
	all := fields.New(schemas)
	s := &memoryStore{
		db:         m,
		location:   location,
		schemas:    schemas,
		references: store.ReferencesOf(schemas),
		Fields:     all,
		evaluator:  newEvaluator(all, schemas),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stores[schemas.Base().Name] = s
	return s
}

// RFC 7643 Section 2.3.7: a service provider MAY enforce referential integrity.
func (m *Memory) detach(target string) []string {
	edges := m.inbound.drop(target)
	detached := make([]string, 0, len(edges))
	for _, e := range edges {
		m.unlink(e, target)
		detached = append(detached, e.source)
	}
	return detached
}

func (m *Memory) unlink(e edge, target string) {
	source := m.entries[e.source]
	for i := range source.links {
		if source.links[i].Attribute == e.attribute {
			source.links[i].Targets = slices.DeleteFunc(source.links[i].Targets, func(id string) bool { return id == target })
		}
	}
	store.Unlink(source.owner.schemas, source.body, e.attribute)
	source.raw, _ = json.Marshal(source.body)
	m.entries[e.source] = source
}

func (m *Memory) record(id string, e entry, references []store.Reference) store.Record {
	return store.Record{ID: id, Body: store.Embed(e.raw, m.relations(id, e, references)), Created: e.created, Updated: e.updated, Version: etag.Weak(e.updated)}
}

// RFC 7643 Section 4.1.2: groups is readOnly, derived from Group membership.
func (m *Memory) targets(id string, e entry, reference store.Reference) []string {
	if !reference.Inverse() {
		return slices.Sorted(slices.Values(targetsOf(e.links, reference.Name)))
	}
	sources := []string{}
	for _, sourceID := range m.inbound.sources(id, reference.InverseOf) {
		if slices.Contains(reference.Types(), string(m.entries[sourceID].schema.Name)) {
			sources = append(sources, sourceID)
		}
	}
	slices.Sort(sources)
	return sources
}

func (m *Memory) relations(id string, e entry, references []store.Reference) []store.Relation {
	relations := []store.Relation{}
	for _, reference := range references {
		if reference.Soft() {
			continue
		}
		for _, target := range m.targets(id, e, reference) {
			relations = append(relations, store.Relation{Reference: reference, ID: target, Type: m.kind(target, reference)})
		}
	}
	return relations
}

func (m *Memory) kind(target string, reference store.Reference) string {
	if reference.Inverse() {
		return ""
	}
	return string(m.entries[target].schema.Name)
}
