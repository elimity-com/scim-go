package memory

import (
	"encoding/json"
	"time"

	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/store"
)

type entry struct {
	schema  *core.Schema
	owner   *Store
	body    core.Object
	raw     json.RawMessage
	links   []store.Link
	created time.Time
	updated time.Time
}

// Entry is one resource of a Store: its body without id and meta, its links, and the times its meta derives from.
type Entry struct {
	ID      string
	Body    json.RawMessage
	Links   []store.Link
	Created time.Time
	Updated time.Time
}
