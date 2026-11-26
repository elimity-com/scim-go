package server

import (
	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/store/memory"
)

var NewDatabase = memory.New

func NewRepositoryIn[T core.Resource](db *memory.Memory, endpoint string, schemas core.Schemas) Repository[T] {
	return NewRepository[T](db.Store(endpoint, schemas), schemas)
}

var WithDatabase = WithMemory

func NewMemoryRepository[T core.Resource](endpoint string, schemas core.Schemas) Repository[T] {
	return NewRepository[T](memory.New().Store(endpoint, schemas), schemas)
}
