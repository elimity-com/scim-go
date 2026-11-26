package memory_test

import (
	"testing"

	"github.com/supabase-community/scim-go/pkg/core"
	"github.com/supabase-community/scim-go/pkg/protocol"
	"github.com/supabase-community/scim-go/pkg/scimtest"
	"github.com/supabase-community/scim-go/pkg/server"
)

// TestConformance runs the conformance suite against a server, which keeps its resources in this package's store.
func TestConformance(t *testing.T) {
	t.Parallel()
	config := core.NewServiceProviderConfig().Filtering(protocol.DefaultLimits.MaxCount).Sorting().Patching().Versioning()
	handler := server.New(basePath, config,
		server.WithResource(server.
			NewResource[*core.User]("User", "/Users", core.SchemaUser, core.UserAttributes()...).
			WithExtension(core.SchemaEnterpriseUser, core.EnterpriseUserAttributes()...)),
		server.WithResource(server.NewResource[*core.Group]("Group", "/Groups", core.SchemaGroup, core.GroupAttributes()...)),
	)
	scimtest.NewSuite(scimtest.Serve(t, handler, basePath), scimtest.Strict()).Run(t)
}
