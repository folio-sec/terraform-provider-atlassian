package organization

import (
	"context"
	"fmt"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &userDataSource{}
var _ datasource.DataSourceWithValidateConfig = &userDataSource{}

type userDataSource struct {
	client *organizationclient.Service
}

type userDataSourceModel struct {
	OrganizationID   types.String `tfsdk:"organization_id"`
	DirectoryID      types.String `tfsdk:"directory_id"`
	AccountIDs       types.Set    `tfsdk:"account_ids"`
	DirectoryIDs     types.Set    `tfsdk:"directory_ids"`
	ResourceIDs      types.Set    `tfsdk:"resource_ids"`
	GroupIDs         types.Set    `tfsdk:"group_ids"`
	MFAEnabled       types.Bool   `tfsdk:"mfa_enabled"`
	ClaimStatus      types.String `tfsdk:"claim_status"`
	Status           types.Set    `tfsdk:"status"`
	AccountStatus    types.Set    `tfsdk:"account_status"`
	MembershipStatus types.Set    `tfsdk:"membership_status"`
	RoleIDs          types.Set    `tfsdk:"role_ids"`
	EmailDomains     types.Set    `tfsdk:"email_domains"`
	SearchTerm       types.String `tfsdk:"search_term"`
	Emails           types.Set    `tfsdk:"emails"`
	Users            types.Set    `tfsdk:"users"`
}

type userResultModel struct {
	AccountID        types.String `tfsdk:"account_id"`
	AccountType      types.String `tfsdk:"account_type"`
	Status           types.String `tfsdk:"status"`
	AccountStatus    types.String `tfsdk:"account_status"`
	MembershipStatus types.String `tfsdk:"membership_status"`
	AddedToOrg       types.String `tfsdk:"added_to_org"`
	Name             types.String `tfsdk:"name"`
	Nickname         types.String `tfsdk:"nickname"`
	Email            types.String `tfsdk:"email"`
	EmailVerified    types.Bool   `tfsdk:"email_verified"`
	ClaimStatus      types.String `tfsdk:"claim_status"`
	Picture          types.String `tfsdk:"picture"`
	Avatar           types.String `tfsdk:"avatar"`
	ManagementSource types.String `tfsdk:"management_source"`
	MFAEnabled       types.Bool   `tfsdk:"mfa_enabled"`
	JobTitle         types.String `tfsdk:"job_title"`
	Department       types.String `tfsdk:"department"`
	Organization     types.String `tfsdk:"organization"`
	Location         types.String `tfsdk:"location"`
	TimeZone         types.String `tfsdk:"time_zone"`
}

func NewUsersDataSource() datasource.DataSource {
	return &userDataSource{}
}

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_users"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	filterSet := func(description string) schema.SetAttribute {
		return schema.SetAttribute{Description: description, Optional: true, ElementType: types.StringType}
	}
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}

	resp.Schema = schema.Schema{
		Description: "Searches all pages of an Atlassian organization directory and returns every matching user.",
		Attributes: map[string]schema.Attribute{
			"organization_id":   schema.StringAttribute{Description: "Atlassian organization ID used in the Organization API path.", Required: true},
			"directory_id":      schema.StringAttribute{Description: "Directory ID used in the Organization API path.", Required: true},
			"account_ids":       filterSet("Account IDs to match. Accepts 1 to 10 values."),
			"directory_ids":     filterSet("Directory IDs to match. Accepts 1 to 10 values."),
			"resource_ids":      filterSet("Resource ARIs to match. Accepts 1 to 20 values."),
			"group_ids":         filterSet("Group IDs to match. Accepts 1 to 10 values."),
			"mfa_enabled":       schema.BoolAttribute{Description: "Filter by whether MFA is enabled.", Optional: true},
			"claim_status":      schema.StringAttribute{Description: "Filter by claim status: managed or unmanaged.", Optional: true},
			"status":            filterSet("Composite user statuses to match. Accepts 1 to 4 API-supported values."),
			"account_status":    filterSet("Account lifecycle statuses to match: active, inactive, or closed."),
			"membership_status": filterSet("Organization membership statuses to match: active, suspended, or no_membership."),
			"role_ids":          filterSet("Atlassian role IDs to match. Accepts 1 to 10 API-supported values."),
			"email_domains":     filterSet("Email domains to match. Accepts 1 to 10 values."),
			"search_term":       schema.StringAttribute{Description: "Free-text display name or email search. Mutually exclusive with emails.", Optional: true},
			"emails":            filterSet("Full email addresses to match exactly. Accepts 1 to 100 values and is mutually exclusive with search_term."),
			"users": schema.SetNestedAttribute{
				Description: "All users matching the configured filters. The set is empty when no users match.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"account_id":        computedString("Unique Atlassian account ID."),
					"account_type":      computedString("Atlassian account type."),
					"status":            computedString("Composite user status."),
					"account_status":    computedString("Account lifecycle status."),
					"membership_status": computedString("Organization membership status."),
					"added_to_org":      computedString("ISO-8601 time when the user was added to the organization."),
					"name":              computedString("Full name."),
					"nickname":          computedString("Nickname."),
					"email":             computedString("Email address."),
					"email_verified":    schema.BoolAttribute{Description: "Whether the email address is verified.", Computed: true},
					"claim_status":      computedString("Managed account claim status."),
					"picture":           computedString("Profile picture URL."),
					"avatar":            computedString("Public avatar URL."),
					"management_source": computedString("How the account was added to the directory."),
					"mfa_enabled":       schema.BoolAttribute{Description: "Whether MFA is enabled.", Computed: true},
					"job_title":         computedString("Job title."),
					"department":        computedString("Department."),
					"organization":      computedString("Organization profile field."),
					"location":          computedString("Location."),
					"time_zone":         computedString("Time zone."),
				}},
			},
		},
	}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	atlassianClient, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if atlassianClient.Organization == nil {
		resp.Diagnostics.AddError("Admin API is not configured", "Set admin_api_key or ATLASSIAN_ADMIN_API_KEY to use organization data sources.")
		return
	}
	d.client = atlassianClient.Organization
}

func (d *userDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config userDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateOrganizationUserIdentifiers(config.OrganizationID, config.DirectoryID, types.StringNull())...)

	if !config.SearchTerm.IsNull() && !config.SearchTerm.IsUnknown() {
		if strings.TrimSpace(config.SearchTerm.ValueString()) == "" {
			resp.Diagnostics.AddError("Invalid organization user filters", "search_term must not be empty when configured.")
		}
		if !config.Emails.IsNull() && !config.Emails.IsUnknown() {
			resp.Diagnostics.AddError("Invalid organization user filters", "search_term and emails are mutually exclusive.")
		}
	}
	if !config.ClaimStatus.IsNull() && !config.ClaimStatus.IsUnknown() {
		if _, allowed := map[string]struct{}{"managed": {}, "unmanaged": {}}[config.ClaimStatus.ValueString()]; !allowed {
			resp.Diagnostics.AddError("Invalid organization user filters", "claim_status must be managed or unmanaged.")
		}
	}

	validations := []struct {
		name    string
		value   types.Set
		maximum int
		allowed map[string]struct{}
	}{
		{"account_ids", config.AccountIDs, 10, nil},
		{"directory_ids", config.DirectoryIDs, 10, nil},
		{"resource_ids", config.ResourceIDs, 20, nil},
		{"group_ids", config.GroupIDs, 10, nil},
		{"status", config.Status, 4, stringSet("active", "suspended", "not_invited", "deactivated", "for_deletion")},
		{"account_status", config.AccountStatus, 3, stringSet("active", "inactive", "closed")},
		{"membership_status", config.MembershipStatus, 3, stringSet("active", "suspended", "no_membership")},
		{"role_ids", config.RoleIDs, 10, stringSet("atlassian/user", "atlassian/admin", "atlassian/guest", "atlassian/customer", "atlassian/user-access-admin", "atlassian/contributor", "atlassian/basic", "atlassian/stakeholder", "atlassian/org-admin", "atlassian/site-admin", "atlassian/ai-access")},
		{"email_domains", config.EmailDomains, 10, nil},
		{"emails", config.Emails, 100, nil},
	}
	for _, validation := range validations {
		validateStringSet(validation.name, validation.value, validation.maximum, validation.allowed, resp)
	}
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config userDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.SearchTerm.IsNull() && !config.Emails.IsNull() {
		resp.Diagnostics.AddError("Invalid user filters", "search_term and emails are mutually exclusive.")
		return
	}

	filters := organizationclient.SearchUsersRequest{
		ClaimStatus: stringValue(config.ClaimStatus),
		SearchTerm:  stringValue(config.SearchTerm),
	}
	if !config.MFAEnabled.IsNull() && !config.MFAEnabled.IsUnknown() {
		value := config.MFAEnabled.ValueBool()
		filters.MFAEnabled = &value
	}
	sets := []struct {
		set    types.Set
		target *[]string
	}{
		{config.AccountIDs, &filters.AccountIDs},
		{config.DirectoryIDs, &filters.DirectoryIDs},
		{config.ResourceIDs, &filters.ResourceIDs},
		{config.GroupIDs, &filters.GroupIDs},
		{config.Status, &filters.Status},
		{config.AccountStatus, &filters.AccountStatus},
		{config.MembershipStatus, &filters.MembershipStatus},
		{config.RoleIDs, &filters.RoleIDs},
		{config.EmailDomains, &filters.EmailDomains},
		{config.Emails, &filters.Emails},
	}
	for _, item := range sets {
		if item.set.IsNull() || item.set.IsUnknown() {
			continue
		}
		resp.Diagnostics.Append(item.set.ElementsAs(ctx, item.target, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	users, err := d.client.SearchUsers(ctx, config.OrganizationID.ValueString(), config.DirectoryID.ValueString(), filters)
	if err != nil {
		resp.Diagnostics.AddError("Unable to search organization users", err.Error())
		return
	}
	results := make([]userResultModel, 0, len(users))
	for _, user := range users {
		results = append(results, userResultModel{
			AccountID:        types.StringValue(user.AccountID),
			AccountType:      nullableStringValue(user.AccountType),
			Status:           nullableStringValue(user.Status),
			AccountStatus:    nullableStringValue(user.AccountStatus),
			MembershipStatus: nullableStringValue(user.MembershipStatus),
			AddedToOrg:       nullableStringValue(user.AddedToOrg),
			Name:             nullableStringValue(user.Name),
			Nickname:         nullableStringValue(user.Nickname),
			Email:            nullableStringValue(user.Email),
			EmailVerified:    nullableBoolValue(user.EmailVerified),
			ClaimStatus:      nullableStringValue(user.ClaimStatus),
			Picture:          nullableStringValue(user.Picture),
			Avatar:           nullableStringValue(user.Avatar),
			ManagementSource: nullableStringValue(user.ManagementSource),
			MFAEnabled:       nullableBoolValue(user.MFAEnabled),
			JobTitle:         nullableStringValue(user.JobTitle),
			Department:       nullableStringValue(user.Department),
			Organization:     nullableStringValue(user.Organization),
			Location:         nullableStringValue(user.Location),
			TimeZone:         nullableStringValue(user.TimeZone),
		})
	}
	userSet, diagnostics := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: userResultAttributeTypes()}, results)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Users = userSet
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func userResultAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"account_id":        types.StringType,
		"account_type":      types.StringType,
		"status":            types.StringType,
		"account_status":    types.StringType,
		"membership_status": types.StringType,
		"added_to_org":      types.StringType,
		"name":              types.StringType,
		"nickname":          types.StringType,
		"email":             types.StringType,
		"email_verified":    types.BoolType,
		"claim_status":      types.StringType,
		"picture":           types.StringType,
		"avatar":            types.StringType,
		"management_source": types.StringType,
		"mfa_enabled":       types.BoolType,
		"job_title":         types.StringType,
		"department":        types.StringType,
		"organization":      types.StringType,
		"location":          types.StringType,
		"time_zone":         types.StringType,
	}
}

func stringValue(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func nullableStringValue(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func nullableBoolValue(value *bool) types.Bool {
	if value == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*value)
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func validateStringSet(name string, value types.Set, maximum int, allowed map[string]struct{}, resp *datasource.ValidateConfigResponse) {
	validateStringSetWithSummary("Invalid organization user filters", name, value, maximum, allowed, resp)
}

func validateStringSetWithSummary(summary, name string, value types.Set, maximum int, allowed map[string]struct{}, resp *datasource.ValidateConfigResponse) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	if length := len(value.Elements()); length == 0 || length > maximum {
		resp.Diagnostics.AddError(summary, fmt.Sprintf("%s must contain between 1 and %d values.", name, maximum))
		return
	}
	for _, element := range value.Elements() {
		itemValue, ok := element.(types.String)
		if !ok || itemValue.IsNull() || itemValue.IsUnknown() {
			continue
		}
		item := itemValue.ValueString()
		if strings.TrimSpace(item) == "" {
			resp.Diagnostics.AddError(summary, fmt.Sprintf("%s must not contain empty values.", name))
			continue
		}
		if allowed != nil {
			if _, valid := allowed[item]; !valid {
				resp.Diagnostics.AddError(summary, fmt.Sprintf("%q is not a supported value for %s.", item, name))
			}
		}
	}
}
