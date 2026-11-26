package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/protocol"
	"github.com/supabase-community/scim-go/pkg/server"
	"github.com/supabase-community/scim-go/pkg/store/memory"
)

const (
	basePath   = "/scim/v2"
	enterprise = string(core.SchemaEnterpriseUser)
	unusedID   = "00000000-0000-7000-8000-000000000000"
)

type response struct {
	code int
	etag string
	body map[string]any
}

type population struct {
	manager, user, inner, outer string
}

func (p population) paths() []string {
	return []string{"/Users/" + p.manager, "/Users/" + p.user, "/Groups/" + p.inner, "/Groups/" + p.outer}
}

func TestRestoreBringsBackExportedResources(t *testing.T) {
	t.Parallel()
	original := memory.New()
	handler := newHandler(original)
	p := populate(t, handler)

	copied, copiedHandler := restored(t, original.Export())

	for _, path := range p.paths() {
		want := send(t, handler, http.MethodGet, path, "")
		got := send(t, copiedHandler, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, got.code, path)
		assert.Equal(t, want.body, got.body, path)
		assert.Equal(t, want.etag, got.etag, path)
	}
	assert.Equal(t, original.Export(), copied.Export())
}

func TestRestoredResourcesKeepTheirRules(t *testing.T) {
	t.Parallel()
	original := memory.New()
	p := populate(t, newHandler(original))
	_, handler := restored(t, original.Export())

	duplicate := send(t, handler, http.MethodPost, "/Users", `{"userName":"MANAGER"}`)
	assert.Equal(t, http.StatusConflict, duplicate.code)

	require.Equal(t, http.StatusNoContent, send(t, handler, http.MethodDelete, "/Users/"+p.manager, "").code)
	user := send(t, handler, http.MethodGet, "/Users/"+p.user, "")
	assert.NotContains(t, user.body, enterprise)
	assert.Equal(t, []string{p.inner}, members(send(t, handler, http.MethodGet, "/Groups/"+p.outer, "")))

	cyclic := fmt.Sprintf(`{"schemas":[%q],"Operations":[{"op":"add","path":"members","value":[{"value":%q}]}]}`, protocol.SchemaPatchOp, p.outer)
	assert.Equal(t, http.StatusBadRequest, send(t, handler, http.MethodPatch, "/Groups/"+p.inner, cyclic).code)
}

func TestRestoreReplacesTheContents(t *testing.T) {
	t.Parallel()
	m := memory.New()
	handler := newHandler(m)
	populate(t, handler)

	require.NoError(t, m.Restore(nil))

	assert.Empty(t, m.Export())
	listed := send(t, handler, http.MethodGet, "/Users", "")
	assert.Empty(t, asList(listed.body["Resources"]))
}

func TestRestoreRejectsInvalidEntries(t *testing.T) {
	t.Parallel()
	source := memory.New()
	populate(t, newHandler(source))

	tests := []struct {
		name   string
		change func([]memory.Entry) []memory.Entry
		want   error
	}{
		{"an unknown resource type", func(entries []memory.Entry) []memory.Entry {
			entries[0].ResourceType = "Widget"
			return entries
		}, memory.ErrUnknownResourceType},
		{"a duplicate id", func(entries []memory.Entry) []memory.Entry {
			return append(entries, entries[0])
		}, memory.ErrDuplicateID},
		{"a duplicate unique value", func(entries []memory.Entry) []memory.Entry {
			copied := userEntry(entries)
			copied.ID, copied.Links = unusedID, nil
			return append(entries, copied)
		}, memory.ErrNotUnique},
		{"a link to a missing resource", func(entries []memory.Entry) []memory.Entry {
			removed := userEntry(entries).ID
			kept := entries[:0]
			for _, entry := range entries {
				if entry.ID != removed {
					kept = append(kept, entry)
				}
			}
			return kept
		}, memory.ErrMissingTarget},
		{"a body that is not an object", func(entries []memory.Entry) []memory.Entry {
			entries[0].Body = json.RawMessage(`[]`)
			return entries
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := memory.New()
			handler := newHandler(m)
			kept := create(t, handler, "/Users", `{"userName":"kept"}`)
			before := m.Export()

			err := m.Restore(tc.change(source.Export()))

			require.Error(t, err)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
			assert.Equal(t, before, m.Export())
			assert.Equal(t, http.StatusOK, send(t, handler, http.MethodGet, "/Users/"+kept, "").code)
		})
	}
}

func newHandler(m *memory.Memory) http.Handler {
	return server.New(basePath, core.NewServiceProviderConfig().Patching().Filtering(protocol.DefaultLimits.MaxCount).Sorting().Versioning(),
		server.WithMemory(m),
		server.WithResource(server.
			NewResource[*core.User]("User", "/Users", core.SchemaUser, core.UserAttributes()...).
			WithExtension(core.SchemaEnterpriseUser, core.EnterpriseUserAttributes()...)),
		server.WithResource(server.NewResource[*core.Group]("Group", "/Groups", core.SchemaGroup, core.GroupAttributes()...)),
	)
}

func send(t *testing.T, handler http.Handler, method, path, body string) response {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), method, basePath+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/scim+json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	raw, err := io.ReadAll(recorder.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &decoded))
	}
	return response{code: recorder.Code, etag: recorder.Header().Get("ETag"), body: decoded}
}

func create(t *testing.T, handler http.Handler, path, body string) string {
	t.Helper()
	created := send(t, handler, http.MethodPost, path, body)
	require.Equal(t, http.StatusCreated, created.code, created.body)
	id, ok := created.body["id"].(string)
	require.True(t, ok)
	return id
}

// populate creates a manager, a user it manages, and a group with that user nested in a group with the manager.
func populate(t *testing.T, handler http.Handler) population {
	t.Helper()
	var p population
	p.manager = create(t, handler, "/Users", `{"userName":"manager"}`)
	p.user = create(t, handler, "/Users", fmt.Sprintf(`{"userName":"user",%q:{"manager":{"value":%q}}}`, enterprise, p.manager))
	p.inner = create(t, handler, "/Groups", fmt.Sprintf(`{"displayName":"inner","members":[{"value":%q}]}`, p.user))
	p.outer = create(t, handler, "/Groups", fmt.Sprintf(`{"displayName":"outer","members":[{"value":%q},{"value":%q}]}`, p.inner, p.manager))
	return p
}

func restored(t *testing.T, entries []memory.Entry) (*memory.Memory, http.Handler) {
	t.Helper()
	m := memory.New()
	handler := newHandler(m)
	require.NoError(t, m.Restore(entries))
	return m, handler
}

func members(response response) []string {
	var ids []string
	for _, raw := range asList(response.body["members"]) {
		if member, ok := raw.(map[string]any); ok {
			ids = append(ids, fmt.Sprint(member["value"]))
		}
	}
	return ids
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

// userEntry returns the entry of a user, which a group links to.
func userEntry(entries []memory.Entry) memory.Entry {
	for _, entry := range entries {
		if entry.ResourceType == "User" {
			return entry
		}
	}
	return memory.Entry{}
}
