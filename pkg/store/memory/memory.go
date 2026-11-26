// Package memory provides the in-memory store a server keeps its resources in by default.
package memory

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/supabase-community/scim-go/internal/decode"
	"github.com/supabase-community/scim-go/internal/etag"
	"github.com/supabase-community/scim-go/internal/fields"
	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/store"
)

type Memory struct {
	mu      sync.Mutex
	entries entries
	unique  uniqueIndex
	inbound inbound
	pending map[core.ResourceTypeName]map[string]entry
}

// Entry is one resource of a Memory: its body without id and meta, its links, and the times its meta derives from.
type Entry struct {
	ID           string
	ResourceType core.ResourceTypeName
	Body         json.RawMessage
	Links        []store.Link
	Created      time.Time
	Updated      time.Time
}

func New() *Memory {
	return &Memory{
		entries: entries{},
		unique:  uniqueIndex{},
		inbound: inbound{},
		pending: map[core.ResourceTypeName]map[string]entry{},
	}
}

// NewFromEntries returns a Memory that holds the given entries, e.g. those Entries returned for an earlier Memory.
// It trusts the entries: it does not check uniqueness or links. An entry joins the Memory when Store is called for its resource type.
func NewFromEntries(entries []Entry) (*Memory, error) {
	m := New()
	for _, e := range entries {
		body, err := decode.Object(e.Body)
		if err != nil {
			return nil, fmt.Errorf("memory: entry %s: %w", e.ID, err)
		}
		if m.pending[e.ResourceType] == nil {
			m.pending[e.ResourceType] = map[string]entry{}
		}
		m.pending[e.ResourceType][e.ID] = entry{body: body, raw: e.Body, links: e.Links, created: e.Created, updated: e.Updated}
	}
	return m, nil
}

// Entries returns every resource of the Memory. It must not run while the Memory serves requests.
func (m *Memory) Entries() []Entry {
	all := make([]Entry, 0, len(m.entries))
	for id, e := range m.entries {
		all = append(all, Entry{ID: id, ResourceType: e.schema.Name, Body: e.raw, Links: e.links, Created: e.created, Updated: e.updated})
	}
	return all
}

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
	m.attach(s)
	return s
}

// attach adds the pending entries of the store's resource type, which NewFromEntries could not add without the store's schemas.
func (m *Memory) attach(s *memoryStore) {
	base := s.schemas.Base()
	for id, e := range m.pending[base.Name] {
		e.schema, e.owner = base, s
		m.entries[id] = e
		m.unique.add(indexKeysOf(e, s.Fields), id)
		m.inbound.relink(id, nil, e.links)
	}
	delete(m.pending, base.Name)
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
