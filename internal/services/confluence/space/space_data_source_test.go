package space

import (
	"context"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func readSpaceConfig(t *testing.T, ds *spaceDataSource, model spaceDataSourceModel) (spaceDataSourceModel, datasource.ReadResponse) {
	t.Helper()
	ctx := context.Background()
	schemaResp := spaceDataSourceSchema(t)
	configState := tfsdk.State{Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil), Schema: schemaResp.Schema}
	if diagnostics := configState.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("build config: %v", diagnostics)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schemaResp.Schema}
	state := tfsdk.State{Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil), Schema: schemaResp.Schema}
	resp := datasource.ReadResponse{State: state}
	ds.Read(ctx, datasource.ReadRequest{Config: config}, &resp)
	var result spaceDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diagnostics := resp.State.Get(ctx, &result); diagnostics.HasError() {
			t.Fatalf("read result state: %v", diagnostics)
		}
	}
	return result, resp
}

func spaceDataSourceSchema(t *testing.T) datasource.SchemaResponse {
	t.Helper()
	var resp datasource.SchemaResponse
	(&spaceDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	return resp
}

func validateSpaceConfig(t *testing.T, model spaceDataSourceModel) (datasource.ValidateConfigResponse, datasource.SchemaResponse) {
	t.Helper()
	ctx := context.Background()
	schemaResp := spaceDataSourceSchema(t)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil), Schema: schemaResp.Schema}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("build config: %v", diagnostics)
	}
	config := tfsdk.Config{Raw: state.Raw, Schema: schemaResp.Schema}
	var resp datasource.ValidateConfigResponse
	(&spaceDataSource{}).ValidateConfig(ctx, datasource.ValidateConfigRequest{Config: config}, &resp)
	return resp, schemaResp
}

func baseSpaceModel() spaceDataSourceModel {
	return spaceDataSourceModel{
		ID:                 types.StringNull(),
		Key:                types.StringNull(),
		Name:               types.StringUnknown(),
		Type:               types.StringUnknown(),
		Status:             types.StringUnknown(),
		AuthorID:           types.StringUnknown(),
		SpaceOwnerID:       types.StringUnknown(),
		HomepageID:         types.StringUnknown(),
		CreatedAt:          types.StringUnknown(),
		CurrentActiveAlias: types.StringUnknown(),
		Description:        types.ObjectUnknown(descriptionAttributeTypes()),
	}
}

func TestSpaceDataSourceValidateConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*spaceDataSourceModel)
		wantErr bool
	}{
		{
			name:    "id only is valid",
			mutate:  func(m *spaceDataSourceModel) { m.ID = types.StringValue("10001") },
			wantErr: false,
		},
		{
			name:    "key only is valid",
			mutate:  func(m *spaceDataSourceModel) { m.Key = types.StringValue("DEMO") },
			wantErr: false,
		},
		{
			name: "both id and key is an error",
			mutate: func(m *spaceDataSourceModel) {
				m.ID = types.StringValue("10001")
				m.Key = types.StringValue("DEMO")
			},
			wantErr: true,
		},
		{
			name:    "neither id nor key is an error",
			mutate:  func(m *spaceDataSourceModel) {},
			wantErr: true,
		},
		{
			name:    "blank id is an error",
			mutate:  func(m *spaceDataSourceModel) { m.ID = types.StringValue("  ") },
			wantErr: true,
		},
		{
			name:    "blank key is an error",
			mutate:  func(m *spaceDataSourceModel) { m.Key = types.StringValue(" ") },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := baseSpaceModel()
			tt.mutate(&model)
			resp, _ := validateSpaceConfig(t, model)
			if got := resp.Diagnostics.HasError(); got != tt.wantErr {
				t.Errorf("HasError() = %v, want %v; diagnostics = %v", got, tt.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestSpaceDataSourceReadByID(t *testing.T) {
	t.Parallel()
	fake, server := newFakeConfluence(t)
	fake.spaceByID["10001"] = `{
		"id":"10001","key":"DEMO","name":"Demo Space","type":"global","status":"current",
		"authorId":"712020:author","homepageId":"20001","createdAt":"2026-01-01T00:00:00.000Z",
		"currentActiveAlias":"demo-alias","description":{"plain":{"value":"A demo space.","representation":"plain"}}
	}`
	ds := &spaceDataSource{client: NewService(newTestClient(t, server))}

	model := baseSpaceModel()
	model.ID = types.StringValue("10001")
	result, resp := readSpaceConfig(t, ds, model)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", resp.Diagnostics)
	}
	if result.Key.ValueString() != "DEMO" || result.Name.ValueString() != "Demo Space" {
		t.Fatalf("result = %#v", result)
	}
	if result.SpaceOwnerID.IsNull() != true {
		t.Errorf("space_owner_id = %#v, want null (absent from this response)", result.SpaceOwnerID)
	}
	if result.CurrentActiveAlias.ValueString() != "demo-alias" {
		t.Errorf("current_active_alias = %q, want demo-alias", result.CurrentActiveAlias.ValueString())
	}
}

func TestSpaceDataSourceReadByKeyNotFound(t *testing.T) {
	t.Parallel()
	_, server := newFakeConfluence(t)
	ds := &spaceDataSource{client: NewService(newTestClient(t, server))}

	model := baseSpaceModel()
	model.Key = types.StringValue("MISSING")
	_, resp := readSpaceConfig(t, ds, model)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Read() with an unmatched key did not report an error")
	}
}

func TestSpaceDataSourceConfigureMissingConfluence(t *testing.T) {
	t.Parallel()
	ds := &spaceDataSource{}
	var resp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &client.Client{}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure() with no Confluence family did not report an error")
	}
}
