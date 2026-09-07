package policy

import (
	"context"
	"fmt"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type policyListFixture struct {
	policyReader
	policies []generated.PolicyModel
	filter   *string
}

func (f *policyListFixture) GetPolicies(_ context.Context, _ string, filter *string) ([]generated.PolicyModel, error) {
	f.filter = filter
	return f.policies, nil
}

func TestPoliciesDataSourceCardinality(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := context.Background()
			f := &policyListFixture{}
			for index := range count {
				p := testPolicy(t)
				p.Id = fmt.Sprint(index)
				f.policies = append(f.policies, p)
			}
			d := &policyDataSource{client: f, collection: true}
			var schema datasource.SchemaResponse
			d.Schema(ctx, datasource.SchemaRequest{}, &schema)
			state := tfsdk.State{Schema: schema.Schema, Raw: tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil)}
			model := policiesDataSourceModel{OrganizationID: types.StringValue("org"), Type: types.StringValue("future-type"), Data: types.SetUnknown(types.ObjectType{AttrTypes: policyObjectTypes(policyReadSchema())})}
			if ds := state.Set(ctx, &model); ds.HasError() {
				t.Fatal(ds)
			}
			resp := datasource.ReadResponse{State: state}
			d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if ds := resp.State.Get(ctx, &model); ds.HasError() {
				t.Fatal(ds)
			}
			if model.Data.IsNull() || model.Data.IsUnknown() || len(model.Data.Elements()) != count || f.filter == nil || *f.filter != "future-type" {
				t.Fatalf("state=%v filter=%v", model, f.filter)
			}
		})
	}
}

func TestPolicyDataSourceNotFoundIsError(t *testing.T) {
	ctx := context.Background()
	d := &policyDataSource{client: &fakePolicyClient{get: func(context.Context, string, string) (generated.PolicyModel, error) {
		return generated.PolicyModel{}, &admin.HTTPError{StatusCode: 404}
	}}}
	var schema datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema, Raw: tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil)}
	model := policyDataSourceModel{OrganizationID: types.StringValue("org"), PolicyID: types.StringValue("id"), Data: types.ObjectUnknown(policyObjectTypes(policyReadSchema()))}
	if ds := state.Set(ctx, &model); ds.HasError() {
		t.Fatal(ds)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("missing data source target must be an error")
	}
}
