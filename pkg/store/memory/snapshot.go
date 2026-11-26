package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/supabase-community/scim-go/internal/decode"
	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/store"
)

var (
	// ErrUnknownResourceType rejects an entry whose resource type has no store.
	ErrUnknownResourceType = errors.New("memory: no store holds the resource type")
	// ErrDuplicateID rejects two entries with the same id.
	ErrDuplicateID = errors.New("memory: two entries have the same id")
	// ErrNotUnique rejects two entries that share a value of a unique attribute, per RFC 7643, Section 7.
	ErrNotUnique = errors.New("memory: two entries share a unique value")
	// ErrMissingTarget rejects a link to an id without entry, per RFC 7643, Section 2.3.7.
	ErrMissingTarget = errors.New("memory: a link targets an id without entry")
)

// Entry is one resource as the memory holds it: its body without id, meta and links, its links, and the times that make up its meta.
type Entry struct {
	ID           string
	ResourceType core.ResourceTypeName
	Body         json.RawMessage
	Links        []store.Link
	Created      time.Time
	Updated      time.Time
}

// Export returns every resource the memory holds, ordered by id, for Restore to bring back.
func (m *Memory) Export() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	exported := make([]Entry, 0, len(m.entries))
	for _, id := range slices.Sorted(maps.Keys(m.entries)) {
		e := m.entries[id]
		exported = append(exported, Entry{
			ID:           id,
			ResourceType: e.schema.Name,
			Body:         slices.Clone(e.raw),
			Links:        cloneLinks(e.links),
			Created:      e.created,
			Updated:      e.updated,
		})
	}
	return exported
}

// Restore replaces every resource the memory holds with the given entries, keeping their ids, links and times, and so their versions.
// Each entry's resource type must have a store, so Restore runs after the stores are created, e.g. by server.New.
// It rejects entries that break uniqueness or link to an id without entry, and then leaves the memory as it was.
func (m *Memory) Restore(entries []Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := restoration{stores: m.stores, entries: map[string]entry{}, unique: uniqueIndex{}, inbound: inbound{}}
	for _, e := range entries {
		if err := r.add(e); err != nil {
			return err
		}
	}
	if err := r.checkTargets(); err != nil {
		return err
	}
	m.entries, m.unique, m.inbound = r.entries, r.unique, r.inbound
	return nil
}

func cloneLinks(links []store.Link) []store.Link {
	cloned := make([]store.Link, len(links))
	for i, link := range links {
		link.Types = slices.Clone(link.Types)
		link.Targets = slices.Clone(link.Targets)
		cloned[i] = link
	}
	return cloned
}

type restoration struct {
	stores  map[core.ResourceTypeName]*memoryStore
	entries entries
	unique  uniqueIndex
	inbound inbound
}

func (r *restoration) add(e Entry) error {
	owner, ok := r.stores[e.ResourceType]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownResourceType, e.ResourceType)
	}
	if _, ok := r.entries[e.ID]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateID, e.ID)
	}
	body, err := decode.Object(e.Body)
	if err != nil {
		return fmt.Errorf("memory: entry %s: %w", e.ID, err)
	}
	next := entry{
		schema:  owner.schemas.Base(),
		owner:   owner,
		body:    body,
		raw:     slices.Clone(e.Body),
		links:   cloneLinks(e.Links),
		created: e.Created,
		updated: e.Updated,
	}
	keys := indexKeysOf(next, owner.Fields)
	if attribute := r.unique.conflict(e.ID, keys); attribute != nil {
		return fmt.Errorf("%w: %s of %s", ErrNotUnique, strconv.Quote(attribute.Name), e.ID)
	}
	r.entries[e.ID] = next
	r.unique.add(keys, e.ID)
	r.inbound.relink(e.ID, nil, next.links)
	return nil
}

func (r *restoration) checkTargets() error {
	for _, id := range slices.Sorted(maps.Keys(r.entries)) {
		for _, target := range allTargets(r.entries[id].links) {
			if _, ok := r.entries[target]; !ok {
				return fmt.Errorf("%w: %s links to %s", ErrMissingTarget, id, target)
			}
		}
	}
	return nil
}
