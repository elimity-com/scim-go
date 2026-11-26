package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/server"
	"github.com/supabase-community/scim-go/pkg/store/memory"
)

const basePath = "/scim/v2"

type response struct {
	code int
	etag string
	body map[string]any
}

func TestNewFromEntriesRestoresEntries(t *testing.T) {
	t.Parallel()
	original := memory.New()
	handler := newHandler(original)
	user := create(t, handler, "/Users", `{"userName":"bjensen"}`)
	inner := create(t, handler, "/Groups", fmt.Sprintf(`{"displayName":"inner","members":[{"value":%q}]}`, user))
	outer := create(t, handler, "/Groups", fmt.Sprintf(`{"displayName":"outer","members":[{"value":%q},{"value":%q}]}`, inner, user))

	restored, err := memory.NewFromEntries(original.Entries())
	require.NoError(t, err)
	restoredHandler := newHandler(restored)

	for _, path := range []string{"/Users/" + user, "/Groups/" + inner, "/Groups/" + outer} {
		want := send(t, handler, http.MethodGet, path, "")
		got := send(t, restoredHandler, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, got.code, path)
		assert.Equal(t, want.body, got.body, path)
		assert.Equal(t, want.etag, got.etag, path)
	}
	assert.ElementsMatch(t, original.Entries(), restored.Entries())

	assert.Equal(t, http.StatusConflict, send(t, restoredHandler, http.MethodPost, "/Users", `{"userName":"BJENSEN"}`).code)
	require.Equal(t, http.StatusNoContent, send(t, restoredHandler, http.MethodDelete, "/Users/"+user, "").code)
	assert.NotContains(t, send(t, restoredHandler, http.MethodGet, "/Groups/"+inner, "").body, "members")
}

func TestNewFromEntriesRejectsABodyThatIsNotAnObject(t *testing.T) {
	t.Parallel()
	entries := []memory.Entry{{ID: "1", ResourceType: "User", Body: json.RawMessage(`[]`)}}

	_, err := memory.NewFromEntries(entries)

	require.Error(t, err)
}

func create(t *testing.T, handler http.Handler, path, body string) string {
	t.Helper()
	created := send(t, handler, http.MethodPost, path, body)
	require.Equal(t, http.StatusCreated, created.code, created.body)
	id, ok := created.body["id"].(string)
	require.True(t, ok)
	return id
}

func newHandler(m *memory.Memory) http.Handler {
	return server.New(basePath, core.NewServiceProviderConfig(),
		server.WithResource(server.
			NewResource[*core.User]("User", "/Users", core.SchemaUser, core.UserAttributes()...).
			WithRepositoryFunc(repository[*core.User](m))),
		server.WithResource(server.
			NewResource[*core.Group]("Group", "/Groups", core.SchemaGroup, core.GroupAttributes()...).
			WithRepositoryFunc(repository[*core.Group](m))),
	)
}

func repository[T core.Resource](m *memory.Memory) func(*server.Resource[T]) server.Repository[T] {
	return func(resource *server.Resource[T]) server.Repository[T] {
		schemas := resource.Schemas()
		return server.NewRepository[T](m.Store(resource.Location(), schemas), schemas)
	}
}

func send(t *testing.T, handler http.Handler, method, path, body string) response {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), method, basePath+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/scim+json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var decoded map[string]any
	if recorder.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded))
	}
	return response{code: recorder.Code, etag: recorder.Header().Get("ETag"), body: decoded}
}
