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
}

// Entry is one resource of a Store: its body without id and meta, its links, and the times its meta derives from.
type Entry struct {
	ID      string
	Body    json.RawMessage
	Links   []store.Link
	Created time.Time
	Updated time.Time
}

func New() *Memory {
	return &Memory{
		entries: entries{},
		unique:  uniqueIndex{},
		inbound: inbound{},
	}
}

func (m *Memory) Store(location string, schemas core.Schemas) *Store {
	all := fields.New(schemas)
	return &Store{
		db:         m,
		location:   location,
		schemas:    schemas,
		references: store.ReferencesOf(schemas),
		Fields:     all,
		evaluator:  newEvaluator(all, schemas),
	}
}

// Entries returns every resource of the store's resource type, e.g. to pass to Load of a store of a later Memory.
// It must not run while the Memory serves requests.
func (s *Store) Entries() []Entry {
	ids := s.db.entries.idsOf(s.schemas.Base())
	all := make([]Entry, 0, len(ids))
	for _, id := range ids {
		e := s.db.entries[id]
		all = append(all, Entry{ID: id, Body: e.raw, Links: e.links, Created: e.created, Updated: e.updated})
	}
	return all
}

// Load adds the given entries as resources of the store's resource type. It trusts them: it does not check uniqueness or links.
// It must not run while the Memory serves requests.
func (s *Store) Load(entries []Entry) error {
	bodies := make([]core.Object, len(entries))
	for i, e := range entries {
		body, err := decode.Object(e.Body)
		if err != nil {
			return fmt.Errorf("memory: entry %s: %w", e.ID, err)
		}
		bodies[i] = body
	}
	for i, e := range entries {
		loaded := entry{schema: s.schemas.Base(), owner: s, body: bodies[i], raw: e.Body, links: e.Links, created: e.Created, updated: e.Updated}
		s.db.entries[e.ID] = loaded
		s.db.unique.add(indexKeysOf(loaded, s.Fields), e.ID)
		s.db.inbound.relink(e.ID, nil, e.Links)
	}
	return nil
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
