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

type stores struct {
	users, groups *memory.Store
}

func TestLoadRestoresEntries(t *testing.T) {
	t.Parallel()
	handler, original := newHandler(t, stores{})
	user := create(t, handler, "/Users", `{"userName":"bjensen"}`)
	inner := create(t, handler, "/Groups", fmt.Sprintf(`{"displayName":"inner","members":[{"value":%q}]}`, user))
	outer := create(t, handler, "/Groups", fmt.Sprintf(`{"displayName":"outer","members":[{"value":%q},{"value":%q}]}`, inner, user))

	restoredHandler, restored := newHandler(t, original)

	for _, path := range []string{"/Users/" + user, "/Groups/" + inner, "/Groups/" + outer} {
		want := send(t, handler, http.MethodGet, path, "")
		got := send(t, restoredHandler, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, got.code, path)
		assert.Equal(t, want.body, got.body, path)
		assert.Equal(t, want.etag, got.etag, path)
	}
	assert.Equal(t, original.users.Entries(), restored.users.Entries())
	assert.Equal(t, original.groups.Entries(), restored.groups.Entries())

	assert.Equal(t, http.StatusConflict, send(t, restoredHandler, http.MethodPost, "/Users", `{"userName":"BJENSEN"}`).code)
	require.Equal(t, http.StatusNoContent, send(t, restoredHandler, http.MethodDelete, "/Users/"+user, "").code)
	assert.NotContains(t, send(t, restoredHandler, http.MethodGet, "/Groups/"+inner, "").body, "members")
}

func TestLoadRejectsABodyThatIsNotAnObject(t *testing.T) {
	t.Parallel()
	users := memory.New().Store(basePath+"/Users", core.Schemas{core.NewSchema(core.SchemaUser).WithName("User").With(core.UserAttributes()...)})

	err := users.Load([]memory.Entry{{ID: "1", Body: json.RawMessage(`[]`)}})

	require.Error(t, err)
	assert.Empty(t, users.Entries())
}

func create(t *testing.T, handler http.Handler, path, body string) string {
	t.Helper()
	created := send(t, handler, http.MethodPost, path, body)
	require.Equal(t, http.StatusCreated, created.code, created.body)
	id, ok := created.body["id"].(string)
	require.True(t, ok)
	return id
}

// newHandler returns a handler on a new Memory whose stores hold the entries of the given ones, if any.
func newHandler(t *testing.T, from stores) (http.Handler, stores) {
	t.Helper()
	m := memory.New()
	var created stores
	handler := server.New(basePath, core.NewServiceProviderConfig(),
		server.WithResource(server.
			NewResource[*core.User]("User", "/Users", core.SchemaUser, core.UserAttributes()...).
			WithRepositoryFunc(repository[*core.User](t, m, from.users, &created.users))),
		server.WithResource(server.
			NewResource[*core.Group]("Group", "/Groups", core.SchemaGroup, core.GroupAttributes()...).
			WithRepositoryFunc(repository[*core.Group](t, m, from.groups, &created.groups))),
	)
	return handler, created
}

func repository[T core.Resource](t *testing.T, m *memory.Memory, from *memory.Store, created **memory.Store) func(*server.Resource[T]) server.Repository[T] {
	t.Helper()
	return func(resource *server.Resource[T]) server.Repository[T] {
		schemas := resource.Schemas()
		s := m.Store(resource.Location(), schemas)
		if from != nil {
			require.NoError(t, s.Load(from.Entries()))
		}
		*created = s
		return server.NewRepository[T](s, schemas)
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
