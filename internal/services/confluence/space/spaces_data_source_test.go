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

func spacesDataSourceSchema(t *testing.T) datasource.SchemaResponse {
	t.Helper()
	var resp datasource.SchemaResponse
	(&spacesDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	return resp
}

func validateSpacesConfig(t *testing.T, model spacesDataSourceModel) datasource.ValidateConfigResponse {
	t.Helper()
	ctx := context.Background()
	schemaResp := spacesDataSourceSchema(t)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil), Schema: schemaResp.Schema}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("build config: %v", diagnostics)
	}
	config := tfsdk.Config{Raw: state.Raw, Schema: schemaResp.Schema}
	var resp datasource.ValidateConfigResponse
	(&spacesDataSource{}).ValidateConfig(ctx, datasource.ValidateConfigRequest{Config: config}, &resp)
	resp.Diagnostics.Append(datasourceSchemaStringDiagnostics(ctx, config, schemaResp.Schema.Attributes, nil)...)
	return resp
}

func baseSpacesModel() spacesDataSourceModel {
	return spacesDataSourceModel{
		IDs:            types.SetNull(types.StringType),
		Keys:           types.SetNull(types.StringType),
		Type:           types.StringNull(),
		Status:         types.StringNull(),
		Labels:         types.SetNull(types.StringType),
		FavoritedBy:    types.StringNull(),
		NotFavoritedBy: types.StringNull(),
		Spaces:         types.SetUnknown(types.ObjectType{AttrTypes: spaceResultAttributeTypes()}),
	}
}

func TestSpacesDataSourceValidateConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*spacesDataSourceModel)
		wantErr bool
	}{
		{name: "no filters is valid", mutate: func(*spacesDataSourceModel) {}, wantErr: false},
		{name: "known type is valid", mutate: func(m *spacesDataSourceModel) { m.Type = types.StringValue("global") }, wantErr: false},
		{name: "known status is valid", mutate: func(m *spacesDataSourceModel) { m.Status = types.StringValue("archived") }, wantErr: false},
		{name: "unknown type value is an error", mutate: func(m *spacesDataSourceModel) { m.Type = types.StringValue("not-a-type") }, wantErr: true},
		{name: "unknown status value is an error", mutate: func(m *spacesDataSourceModel) { m.Status = types.StringValue("not-a-status") }, wantErr: true},
		{name: "blank favorited_by is an error", mutate: func(m *spacesDataSourceModel) { m.FavoritedBy = types.StringValue(" ") }, wantErr: true},
		{name: "blank not_favorited_by is an error", mutate: func(m *spacesDataSourceModel) { m.NotFavoritedBy = types.StringValue("") }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := baseSpacesModel()
			tt.mutate(&model)
			resp := validateSpacesConfig(t, model)
			if got := resp.Diagnostics.HasError(); got != tt.wantErr {
				t.Errorf("HasError() = %v, want %v; diagnostics = %v", got, tt.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestSpacesDataSourceSchemaExposesOnlyMatchingFilters(t *testing.T) {
	t.Parallel()
	resp := spacesDataSourceSchema(t)

	for _, name := range []string{"ids", "keys", "type", "status", "labels", "favorited_by", "not_favorited_by", "spaces"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("schema attribute %s is missing", name)
		}
	}
	for _, name := range []string{"sort", "cursor", "limit", "description_format", "include_icon"} {
		if _, ok := resp.Schema.Attributes[name]; ok {
			t.Errorf("schema exposes response-shaping attribute %s, which must stay internal", name)
		}
	}
}

func TestSpacesDataSourceReadReturnsEveryPage(t *testing.T) {
	t.Parallel()
	fake, server := newFakeConfluence(t)
	fake.pages = []string{
		`{"results":[{"id":"1","key":"A","name":"A","type":"global","status":"current","authorId":"acc-1"}],
		  "_links":{"next":"/wiki/api/v2/spaces?cursor=page-2"}}`,
		`{"results":[{"id":"2","key":"B","name":"B","type":"global","status":"archived","authorId":"acc-1"}]}`,
	}
	ds := &spacesDataSource{client: NewService(newTestClient(t, server))}

	ctx := context.Background()
	schemaResp := spacesDataSourceSchema(t)
	model := baseSpacesModel()
	configState := tfsdk.State{Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil), Schema: schemaResp.Schema}
	if diagnostics := configState.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("build config: %v", diagnostics)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schemaResp.Schema}
	state := tfsdk.State{Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil), Schema: schemaResp.Schema}
	resp := datasource.ReadResponse{State: state}
	ds.Read(ctx, datasource.ReadRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", resp.Diagnostics)
	}

	var result spacesDataSourceModel
	if diagnostics := resp.State.Get(ctx, &result); diagnostics.HasError() {
		t.Fatalf("read result state: %v", diagnostics)
	}
	var spaces []spaceResultModel
	if diagnostics := result.Spaces.ElementsAs(ctx, &spaces, false); diagnostics.HasError() {
		t.Fatalf("decode spaces: %v", diagnostics)
	}
	if len(spaces) != 2 {
		t.Fatalf("spaces = %#v, want 2 entries (including the archived one; no status filter was set)", spaces)
	}
}

func TestSpacesDataSourceConfigureMissingConfluence(t *testing.T) {
	t.Parallel()
	ds := &spacesDataSource{}
	var resp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &client.Client{}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure() with no Confluence family did not report an error")
	}
}
