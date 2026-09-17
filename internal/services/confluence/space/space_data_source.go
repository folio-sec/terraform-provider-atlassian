package space

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &spaceDataSource{}
var _ datasource.DataSourceWithValidateConfig = &spaceDataSource{}

type spaceDataSource struct {
	client *Service
}

type spaceDataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Key                types.String `tfsdk:"key"`
	Name               types.String `tfsdk:"name"`
	Type               types.String `tfsdk:"type"`
	Status             types.String `tfsdk:"status"`
	AuthorID           types.String `tfsdk:"author_id"`
	SpaceOwnerID       types.String `tfsdk:"space_owner_id"`
	HomepageID         types.String `tfsdk:"homepage_id"`
	CreatedAt          types.String `tfsdk:"created_at"`
	CurrentActiveAlias types.String `tfsdk:"current_active_alias"`
	Description        types.Object `tfsdk:"description"`
}

// NewSpaceDataSource returns the atlassian_confluence_space data source.
func NewSpaceDataSource() datasource.DataSource {
	return &spaceDataSource{}
}

func (d *spaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space"
}

func (d *spaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves one Confluence Cloud space by ID or by key. Exactly one of `id` or `key` must be set.\n\n" +
			"## Required OAuth scopes\n\n" +
			"When the provider authenticates as a service account, its credential must carry:\n\n" +
			"- `read:space:confluence`\n",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Numeric-string ID of the space. Exactly one of id or key is required; the other is computed from the lookup.",
				Optional:    true,
				Computed:    true,
				Validators:  []validator.String{nonBlank},
			},
			"key": schema.StringAttribute{
				Description: "Key of the space. Exactly one of id or key is required; the other is computed from the lookup.",
				Optional:    true,
				Computed:    true,
				Validators:  []validator.String{nonBlank},
			},
			"name":                 computedString("Name of the space."),
			"type":                 computedString("Type of the space."),
			"status":               computedString("Status of the space."),
			"author_id":            computedString("Account ID of the user who created the space."),
			"space_owner_id":       computedString("Account ID of the user who owns the space. Absent on some responses."),
			"homepage_id":          computedString("ID of the space's homepage."),
			"created_at":           computedString("Date and time the space was created, RFC 3339."),
			"current_active_alias": computedString("Currently active alias for the space. Returned by the API even though the published v2 schema omits it."),
			"description": schema.SingleNestedAttribute{
				Description: "Space description in the plain representation. Null when the space has no description.",
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"value":          computedString("Description text."),
					"representation": computedString("Representation of value. Only plain is currently returned."),
				},
			},
		},
	}
}

func (d *spaceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	atlassianClient, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if atlassianClient.Confluence == nil {
		resp.Diagnostics.AddError("Confluence is not configured", "Set site_url plus basic_auth or service_account to use Confluence data sources.")
		return
	}
	d.client = NewService(atlassianClient.Confluence)
}

func (d *spaceDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config spaceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Each attribute's blank check is a schema validator. This rule reads both
	// attributes at once and treats a blank value as unset, which an attribute
	// validator cannot express.
	const summary = "Invalid Confluence space lookup"
	idSet := setNonBlank(config.ID)
	keySet := setNonBlank(config.Key)
	switch {
	case idSet && keySet:
		resp.Diagnostics.AddError(summary, "id and key are mutually exclusive; set exactly one.")
	case !idSet && !keySet && !config.ID.IsUnknown() && !config.Key.IsUnknown():
		// An unknown value (fed from another resource) is left to Terraform;
		// only both being known and absent is an error here.
		resp.Diagnostics.AddError(summary, "exactly one of id or key must be set.")
	}
}

// setNonBlank reports whether a configuration value is known, non-null, and
// non-blank.
func setNonBlank(value types.String) bool {
	return knownString(value) && strings.TrimSpace(value.ValueString()) != ""
}

func (d *spaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config spaceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var (
		result Space
		err    error
	)
	switch {
	case setNonBlank(config.ID):
		result, err = d.client.GetSpaceByID(ctx, config.ID.ValueString())
	case setNonBlank(config.Key):
		result, err = d.client.GetSpaceByKey(ctx, config.Key.ValueString())
	default:
		resp.Diagnostics.AddError("Invalid Confluence space lookup", "exactly one of id or key must be set.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence space", err.Error())
		return
	}

	description, err := descriptionValue(ctx, result.Description)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence space", err.Error())
		return
	}
	state := spaceDataSourceModel{
		ID:                 types.StringValue(result.ID),
		Key:                types.StringValue(result.Key),
		Name:               types.StringValue(result.Name),
		Type:               types.StringValue(result.Type),
		Status:             types.StringValue(result.Status),
		AuthorID:           types.StringValue(result.AuthorID),
		SpaceOwnerID:       nullableStringPointer(result.SpaceOwnerID),
		HomepageID:         nullableStringPointer(result.HomepageID),
		CreatedAt:          createdAtValue(result.CreatedAt),
		CurrentActiveAlias: nullableStringPointer(result.CurrentActiveAlias),
		Description:        description,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func descriptionAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"value":          types.StringType,
		"representation": types.StringType,
	}
}

type descriptionModel struct {
	Value          types.String `tfsdk:"value"`
	Representation types.String `tfsdk:"representation"`
}

func descriptionValue(ctx context.Context, description *Description) (types.Object, error) {
	if description == nil {
		return types.ObjectNull(descriptionAttributeTypes()), nil
	}
	value, diagnostics := types.ObjectValueFrom(ctx, descriptionAttributeTypes(), descriptionModel{
		Value:          types.StringValue(description.Value),
		Representation: types.StringValue(description.Representation),
	})
	if diagnostics.HasError() {
		return types.ObjectNull(descriptionAttributeTypes()), fmt.Errorf("build description object: %s", diagnosticsSummary(diagnostics))
	}
	return value, nil
}

func diagnosticsSummary(diagnostics diag.Diagnostics) string {
	if len(diagnostics) == 0 {
		return ""
	}
	return diagnostics[0].Summary() + ": " + diagnostics[0].Detail()
}

func nullableStringPointer(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func createdAtValue(t *time.Time) types.String {
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339Nano))
}
