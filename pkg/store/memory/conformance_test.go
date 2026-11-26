package memory_test

import (
	"testing"

	"github.com/supabase-community/scim-go/pkg/scimtest"
	"github.com/supabase-community/scim-go/pkg/store/memory"
)

func TestConformance(t *testing.T) {
	t.Parallel()
	client := scimtest.Serve(t, newHandler(memory.New()), basePath)
	scimtest.NewSuite(client, scimtest.Strict()).Run(t)
}
