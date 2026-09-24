package space

import (
	"context"
	"fmt"

	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"

	"github.com/folio-sec/terraform-provider-atlassian/internal/validation"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &spacesDataSource{}

type spacesDataSource struct {
	client *Service
}

type spacesDataSourceModel struct {
	IDs            types.Set    `tfsdk:"ids"`
	Keys           types.Set    `tfsdk:"keys"`
	Type           types.String `tfsdk:"type"`
	Status         types.String `tfsdk:"status"`
	Labels         types.Set    `tfsdk:"labels"`
	FavoritedBy    types.String `tfsdk:"favorited_by"`
	NotFavoritedBy types.String `tfsdk:"not_favorited_by"`
	Spaces         types.Set    `tfsdk:"spaces"`
}

type spaceResultModel struct {
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

// NewSpacesDataSource returns the atlassian_confluence_spaces data source.
func NewSpacesDataSource() datasource.DataSource {
	return &spacesDataSource{}
}

func (d *spacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_spaces"
}

func (d *spacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	filterSet := func(description string) schema.SetAttribute {
		return schema.SetAttribute{Description: description, Optional: true, ElementType: types.StringType}
	}
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Returns every Confluence Cloud space matching the configured filters. " +
			"**Archived spaces are included unless `status` is used to filter them out**: with no status filter, " +
			"the API returns archived spaces alongside current ones. " +
			"Ordering is not exposed, so the result is a set at every cardinality, including zero or one match.\n\n" +
			"## Required OAuth scopes\n\n" +
			"When the provider authenticates as a service account, its credential must carry:\n\n" +
			"- `read:space:confluence`\n",
		Attributes: map[string]schema.Attribute{
			"ids":              filterSet("Space IDs to match."),
			"keys":             filterSet("Space keys to match."),
			"type":             schema.StringAttribute{Description: "Space type to match.", Optional: true, Validators: []validator.String{validation.Enum[v2gen.GetSpacesParamsType]()}},
			"status":           schema.StringAttribute{Description: "Space status to match. Set this to exclude archived spaces from the result.", Optional: true, Validators: []validator.String{validation.Enum[v2gen.GetSpacesParamsStatus]()}},
			"labels":           filterSet("Space labels to match."),
			"favorited_by":     schema.StringAttribute{Description: "Account ID of a user; matches spaces that user has favorited.", Optional: true, Validators: []validator.String{validation.NonBlank}},
			"not_favorited_by": schema.StringAttribute{Description: "Account ID of a user; matches spaces that user has not favorited.", Optional: true, Validators: []validator.String{validation.NonBlank}},
			"spaces": schema.SetNestedAttribute{
				Description: "Every space matching the configured filters. The set is empty when no space matches. " +
					"Includes archived spaces unless status is used to filter them out.",
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":                   computedString("Numeric-string ID of the space."),
					"key":                  computedString("Key of the space."),
					"name":                 computedString("Name of the space."),
					"type":                 computedString("Type of the space."),
					"status":               computedString("Status of the space."),
					"author_id":            computedString("Account ID of the user who created the space."),
					"space_owner_id":       computedString("Account ID of the user who owns the space. Absent on some responses."),
					"homepage_id":          computedString("ID of the space's homepage."),
					"created_at":           computedString("Date and time the space was created, RFC 3339."),
					"current_active_alias": computedString("Currently active alias for the space."),
					"description": schema.SingleNestedAttribute{
						Description: "Space description in the plain representation. Null when the space has no description.",
						Computed:    true,
						Attributes: map[string]schema.Attribute{
							"value":          computedString("Description text."),
							"representation": computedString("Representation of value. Only plain is currently returned."),
						},
					},
				}},
			},
		},
	}
}

func (d *spacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *spacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config spacesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	filters := GetSpacesFilters{
		Type:           stringValue(config.Type),
		Status:         stringValue(config.Status),
		FavoritedBy:    stringValue(config.FavoritedBy),
		NotFavoritedBy: stringValue(config.NotFavoritedBy),
	}
	for _, item := range []struct {
		set    types.Set
		target *[]string
	}{
		{config.IDs, &filters.IDs},
		{config.Keys, &filters.Keys},
		{config.Labels, &filters.Labels},
	} {
		if item.set.IsNull() || item.set.IsUnknown() {
			continue
		}
		resp.Diagnostics.Append(item.set.ElementsAs(ctx, item.target, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	spaces, err := d.client.GetSpaces(ctx, filters)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list Confluence spaces", err.Error())
		return
	}

	results := make([]spaceResultModel, 0, len(spaces))
	for _, s := range spaces {
		description, err := descriptionValue(ctx, s.Description)
		if err != nil {
			resp.Diagnostics.AddError("Unable to list Confluence spaces", err.Error())
			return
		}
		results = append(results, spaceResultModel{
			ID:                 types.StringValue(s.ID),
			Key:                types.StringValue(s.Key),
			Name:               types.StringValue(s.Name),
			Type:               types.StringValue(s.Type),
			Status:             types.StringValue(s.Status),
			AuthorID:           types.StringValue(s.AuthorID),
			SpaceOwnerID:       nullableStringPointer(s.SpaceOwnerID),
			HomepageID:         nullableStringPointer(s.HomepageID),
			CreatedAt:          createdAtValue(s.CreatedAt),
			CurrentActiveAlias: nullableStringPointer(s.CurrentActiveAlias),
			Description:        description,
		})
	}
	if resp.Diagnostics.HasError() {
		return
	}
	spacesSet, diagnostics := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: spaceResultAttributeTypes()}, results)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Spaces = spacesSet
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func stringValue(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func spaceResultAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":                   types.StringType,
		"key":                  types.StringType,
		"name":                 types.StringType,
		"type":                 types.StringType,
		"status":               types.StringType,
		"author_id":            types.StringType,
		"space_owner_id":       types.StringType,
		"homepage_id":          types.StringType,
		"created_at":           types.StringType,
		"current_active_alias": types.StringType,
		"description":          types.ObjectType{AttrTypes: descriptionAttributeTypes()},
	}
}
