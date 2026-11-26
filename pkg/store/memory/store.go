package memory

import (
	"context"
	"strconv"

	"github.com/supabase-community/scim-go/internal/decode"
	"github.com/supabase-community/scim-go/internal/etag"
	"github.com/supabase-community/scim-go/internal/fields"
	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/protocol"
	"github.com/supabase-community/scim-go/pkg/scimerrors"
	"github.com/supabase-community/scim-go/pkg/store"
)

type Store struct {
	db         *Memory
	location   string
	schemas    core.Schemas
	references []store.Reference
	fields.Fields
	evaluator protocol.Evaluator[predicate]
}

func (s *Store) List(_ context.Context, request *protocol.SearchRequest, references []store.Reference) ([]store.Record, int, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	return query{s, request}.run(references)
}

func (s *Store) Find(_ context.Context, id string, references []store.Reference) (store.Record, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	e, ok := s.db.entries.find(id, s.schemas.Base())
	if !ok {
		return store.Record{}, scimerrors.ErrNotFound("Not found")
	}
	return s.db.record(id, e, references), nil
}

func (s *Store) Create(_ context.Context, doc store.Document) (store.Record, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	at := now()
	return s.save(doc, entry{created: at, updated: at})
}

func (s *Store) Update(_ context.Context, doc store.Document, version string) (store.Record, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	current, err := s.locate(doc.ID, version)
	if err != nil {
		return store.Record{}, err
	}
	return s.save(doc, entry{created: current.created, updated: bumped(current.updated)})
}

func (s *Store) Delete(_ context.Context, id, version string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	current, err := s.locate(id, version)
	if err != nil {
		return err
	}
	s.db.unique.remove(indexKeysOf(current, s.Fields))
	s.db.inbound.relink(id, current.links, nil)
	delete(s.db.entries, id)
	s.db.entries.touch(append(s.db.detach(id), allTargets(current.links)...))
	return nil
}

// RFC 7644 Section 3.14: a non-empty version must match the stored one.
func (s *Store) locate(id, version string) (entry, error) {
	e, ok := s.db.entries.find(id, s.schemas.Base())
	if !ok || (version != "" && etag.Weak(e.updated) != version) {
		return entry{}, store.ErrNoMatch
	}
	return e, nil
}

func (s *Store) save(doc store.Document, next entry) (store.Record, error) {
	body, err := decode.Object(doc.Body)
	if err != nil {
		return store.Record{}, err
	}
	before := s.db.entries[doc.ID]
	next.schema, next.owner, next.body, next.raw, next.links = s.schemas.Base(), s, body, doc.Body, s.db.entries.resolved(doc.Links)
	keys, err := s.verify(doc, before, next)
	if err != nil {
		return store.Record{}, err
	}
	s.commit(doc, before, next, keys)
	return s.db.record(doc.ID, next, doc.References), nil
}

func (s *Store) verify(doc store.Document, before, next entry) ([]indexKey, error) {
	if err := s.db.entries.checkLinks(doc.ID, before.links, doc.Links); err != nil {
		return nil, err
	}
	keys := indexKeysOf(next, s.Fields)
	if attribute := s.db.unique.conflict(doc.ID, keys); attribute != nil {
		return nil, scimerrors.ErrUniqueness(strconv.Quote(attribute.Name) + " must be unique")
	}
	return keys, nil
}

func (s *Store) commit(doc store.Document, before, next entry, keys []indexKey) {
	changed := changedTargets(before.links, doc.Links)
	s.put(doc.ID, before, next, keys)
	s.db.entries.touch(changed)
}

// put stores next under id in place of before, keeping the indexes in step.
func (s *Store) put(id string, before, next entry, keys []indexKey) {
	s.db.unique.remove(indexKeysOf(before, s.Fields))
	s.db.inbound.relink(id, before.links, next.links)
	s.db.entries[id] = next
	s.db.unique.add(keys, id)
}
