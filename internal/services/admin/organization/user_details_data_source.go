package organization

import (
	"context"
	"fmt"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &userDetailsDataSource{}
var _ datasource.DataSourceWithValidateConfig = &userDetailsDataSource{}

type userDetailsDataSource struct {
	client *organizationclient.Service
}

type userDetailsDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	OrganizationID   types.String `tfsdk:"organization_id"`
	DirectoryID      types.String `tfsdk:"directory_id"`
	AccountID        types.String `tfsdk:"account_id"`
	AccountType      types.String `tfsdk:"account_type"`
	Status           types.String `tfsdk:"status"`
	AccountStatus    types.String `tfsdk:"account_status"`
	MembershipStatus types.String `tfsdk:"membership_status"`
	AddedToOrg       types.String `tfsdk:"added_to_org"`
	DeactivatedOn    types.String `tfsdk:"deactivated_on"`
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
	PlatformRoles    types.Set    `tfsdk:"platform_roles"`
}

func NewUserDataSource() datasource.DataSource {
	return &userDetailsDataSource{}
}

func (d *userDetailsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_user"
}

func (d *userDetailsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		Description: "Retrieves one Atlassian organization user by account ID.",
		Attributes: map[string]schema.Attribute{
			"id":                computedString("Data source ID, equal to the Atlassian account ID."),
			"organization_id":   schema.StringAttribute{Description: "Atlassian organization ID used in the Organization API path.", Required: true},
			"directory_id":      schema.StringAttribute{Description: "Directory containing the user.", Required: true},
			"account_id":        schema.StringAttribute{Description: "Unique Atlassian account ID.", Required: true},
			"account_type":      computedString("Atlassian account type."),
			"status":            computedString("Composite user status."),
			"account_status":    computedString("Account lifecycle status."),
			"membership_status": computedString("Organization membership status."),
			"added_to_org":      computedString("ISO-8601 time when the user was added to the organization."),
			"deactivated_on":    computedString("ISO-8601 time when the user was deactivated."),
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
			"platform_roles":    schema.SetAttribute{Description: "Organization-level Atlassian admin role IDs assigned to the user.", Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *userDetailsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *userDetailsDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config userDetailsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateOrganizationUserIdentifiers(config.OrganizationID, config.DirectoryID, config.AccountID)...)
}

func (d *userDetailsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config userDetailsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, err := d.client.GetUser(ctx, config.OrganizationID.ValueString(), config.DirectoryID.ValueString(), config.AccountID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization user", err.Error())
		return
	}

	config.ID = types.StringValue(user.AccountID)
	config.AccountID = types.StringValue(user.AccountID)
	config.AccountType = nullableStringValue(user.AccountType)
	config.Status = nullableStringValue(user.Status)
	config.AccountStatus = nullableStringValue(user.AccountStatus)
	config.MembershipStatus = nullableStringValue(user.MembershipStatus)
	config.AddedToOrg = nullableStringValue(user.AddedToOrg)
	config.DeactivatedOn = nullableStringValue(user.DeactivatedOn)
	config.Name = nullableStringValue(user.Name)
	config.Nickname = nullableStringValue(user.Nickname)
	config.Email = nullableStringValue(user.Email)
	config.EmailVerified = nullableBoolValue(user.EmailVerified)
	config.ClaimStatus = nullableStringValue(user.ClaimStatus)
	config.Picture = nullableStringValue(user.Picture)
	config.Avatar = nullableStringValue(user.Avatar)
	config.ManagementSource = nullableStringValue(user.ManagementSource)
	config.MFAEnabled = nullableBoolValue(user.MFAEnabled)
	config.JobTitle = nullableStringValue(user.JobTitle)
	config.Department = nullableStringValue(user.Department)
	config.Organization = nullableStringValue(user.Organization)
	config.Location = nullableStringValue(user.Location)
	config.TimeZone = nullableStringValue(user.TimeZone)
	if user.PlatformRoles == nil {
		config.PlatformRoles = types.SetNull(types.StringType)
	} else {
		platformRoles, diagnostics := types.SetValueFrom(ctx, types.StringType, *user.PlatformRoles)
		resp.Diagnostics.Append(diagnostics...)
		config.PlatformRoles = platformRoles
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func validateOrganizationUserIdentifiers(organizationID, directoryID, accountID types.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range []struct {
		name  string
		value types.String
	}{
		{"organization_id", organizationID},
		{"directory_id", directoryID},
		{"account_id", accountID},
	} {
		if !item.value.IsNull() && !item.value.IsUnknown() && strings.TrimSpace(item.value.ValueString()) == "" {
			diagnostics.AddError("Invalid organization user", fmt.Sprintf("%s must not be empty.", item.name))
		}
	}
	return diagnostics
}
