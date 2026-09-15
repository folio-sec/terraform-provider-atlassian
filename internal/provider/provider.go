package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	datasecuritypolicyservice "github.com/folio-sec/terraform-provider-atlassian/internal/services/admin/control/data_security_policy"
	organizationservice "github.com/folio-sec/terraform-provider-atlassian/internal/services/admin/organization"
	organizationpolicyservice "github.com/folio-sec/terraform-provider-atlassian/internal/services/admin/organization/policy"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name atlassian --provider-dir ../..
//go:generate go run ../cmd/postprocess-docs -docs-dir ../../docs

var (
	_ provider.Provider                   = &AtlassianProvider{}
	_ provider.ProviderWithValidateConfig = &AtlassianProvider{}
)

// Environment variables that back each credential attribute. They are the
// names Atlassian's own tooling uses, so a value may already be present in a
// shell for another tool; the Configure diagnostics therefore always say
// which source a value came from.
const (
	envAdminAPIKey  = "ATLASSIAN_ADMIN_API_KEY" //nolint:gosec // The name of an environment variable, not a credential.
	envSiteURL      = "ATLASSIAN_SITE_URL"
	envEmail        = "ATLASSIAN_EMAIL"
	envAPIToken     = "ATLASSIAN_API_TOKEN" //nolint:gosec // The name of an environment variable, not a credential.
	envClientID     = "ATLASSIAN_CLIENT_ID"
	envClientSecret = "ATLASSIAN_CLIENT_SECRET"
	envCloudID      = "ATLASSIAN_CLOUD_ID"
)

const (
	attrBasicAuth      = "basic_auth"
	attrServiceAccount = "service_account"
	attrSiteURL        = "site_url"
)

type AtlassianProvider struct {
	version string
}

type providerModel struct {
	AdminAPIKey    types.String `tfsdk:"admin_api_key"`
	SiteURL        types.String `tfsdk:"site_url"`
	BasicAuth      types.Object `tfsdk:"basic_auth"`
	ServiceAccount types.Object `tfsdk:"service_account"`
}

type basicAuthModel struct {
	Email    types.String `tfsdk:"email"`
	APIToken types.String `tfsdk:"api_token"`
}

type serviceAccountModel struct {
	ClientID     types.String `tfsdk:"client_id"`
	ClientSecret types.String `tfsdk:"client_secret"`
	CloudID      types.String `tfsdk:"cloud_id"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AtlassianProvider{version: version}
	}
}

func (p *AtlassianProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "atlassian"
	resp.Version = p.version
}

func (p *AtlassianProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage Atlassian Cloud resources. Cloud Admin API types authenticate with an organization API key; Confluence types authenticate as a site user with an API token or as a service account with OAuth 2.0 client credentials. Configure only the families you use.",
		Attributes: map[string]schema.Attribute{
			"admin_api_key": schema.StringAttribute{
				Description: "Atlassian organization API key used for Cloud Admin APIs. May also be set with " + envAdminAPIKey + ". Leave unset when only Confluence types are used.",
				Optional:    true,
				Sensitive:   true,
			},
			attrSiteURL: schema.StringAttribute{
				Description: "Confluence Cloud site URL, for example https://example.atlassian.net. A bare host is accepted. Must not carry a path, query, or fragment. Required with basic_auth; with service_account it is only needed when cloud_id is omitted, to discover it. May also be set with " + envSiteURL + ".",
				Optional:    true,
			},
			attrBasicAuth: schema.SingleNestedAttribute{
				Description: "Authenticate to Confluence as an Atlassian account using HTTP basic auth with an API token. Exactly one of basic_auth and service_account may be configured.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"email": schema.StringAttribute{
						Description: "Email of the Atlassian account. May also be set with " + envEmail + ".",
						Optional:    true,
					},
					"api_token": schema.StringAttribute{
						Description: "API token created for the account at id.atlassian.com. May also be set with " + envAPIToken + ".",
						Optional:    true,
						Sensitive:   true,
					},
				},
			},
			attrServiceAccount: schema.SingleNestedAttribute{
				Description: "Authenticate to Confluence as a service account using OAuth 2.0 client credentials. Requests are sent through the api.atlassian.com gateway, so the site is identified by cloud_id. Exactly one of basic_auth and service_account may be configured.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"client_id": schema.StringAttribute{
						Description: "OAuth 2.0 client ID of the service account credential. May also be set with " + envClientID + ".",
						Optional:    true,
					},
					"client_secret": schema.StringAttribute{
						Description: "OAuth 2.0 client secret of the service account credential. May also be set with " + envClientSecret + ".",
						Optional:    true,
						Sensitive:   true,
					},
					"cloud_id": schema.StringAttribute{
						Description: "Cloud ID of the Confluence site, a lowercase UUID. When omitted it is discovered from site_url on first use. May also be set with " + envCloudID + ".",
						Optional:    true,
					},
				},
			},
		},
	}
}

// ValidateConfig checks the shape of values written in configuration. It
// cannot see environment variables, so presence and combination rules live in
// Configure, after the two sources are merged.
func (p *AtlassianProvider) ValidateConfig(ctx context.Context, req provider.ValidateConfigRequest, resp *provider.ValidateConfigResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if knownString(config.SiteURL) {
		resp.Diagnostics.Append(validateSiteURL(path.Root(attrSiteURL), config.SiteURL.ValueString())...)
	}

	if basic, ok := objectAs[basicAuthModel](ctx, config.BasicAuth, &resp.Diagnostics); ok {
		root := path.Root(attrBasicAuth)
		resp.Diagnostics.Append(validateNonEmpty(root.AtName("email"), basic.Email)...)
		resp.Diagnostics.Append(validateNonEmpty(root.AtName("api_token"), basic.APIToken)...)
		if knownString(basic.Email) && strings.TrimSpace(basic.Email.ValueString()) != "" {
			resp.Diagnostics.Append(validateEmail(root.AtName("email"), basic.Email.ValueString())...)
		}
	}

	if account, ok := objectAs[serviceAccountModel](ctx, config.ServiceAccount, &resp.Diagnostics); ok {
		root := path.Root(attrServiceAccount)
		resp.Diagnostics.Append(validateNonEmpty(root.AtName("client_id"), account.ClientID)...)
		resp.Diagnostics.Append(validateNonEmpty(root.AtName("client_secret"), account.ClientSecret)...)
		resp.Diagnostics.Append(validateNonEmpty(root.AtName("cloud_id"), account.CloudID)...)
		if knownString(account.CloudID) && strings.TrimSpace(account.CloudID.ValueString()) != "" {
			resp.Diagnostics.Append(validateCloudID(root.AtName("cloud_id"), account.CloudID.ValueString())...)
		}
	}
}

func (p *AtlassianProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A value fed from another resource is unknown during an early plan.
	// Leave provider data unset rather than mistaking unknown for absent; the
	// data sources and resources tolerate nil provider data.
	if config.AdminAPIKey.IsUnknown() || config.SiteURL.IsUnknown() || config.BasicAuth.IsUnknown() || config.ServiceAccount.IsUnknown() {
		return
	}
	var basic basicAuthModel
	var account serviceAccountModel
	if !config.BasicAuth.IsNull() {
		resp.Diagnostics.Append(config.BasicAuth.As(ctx, &basic, basetypes.ObjectAsOptions{})...)
	}
	if !config.ServiceAccount.IsNull() {
		resp.Diagnostics.Append(config.ServiceAccount.As(ctx, &account, basetypes.ObjectAsOptions{})...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if basic.Email.IsUnknown() || basic.APIToken.IsUnknown() || account.ClientID.IsUnknown() || account.ClientSecret.IsUnknown() || account.CloudID.IsUnknown() {
		return
	}

	credentials := resolvedCredentials{
		adminAPIKey:  resolve(config.AdminAPIKey, envAdminAPIKey),
		siteURL:      resolve(config.SiteURL, envSiteURL),
		email:        resolve(basic.Email, envEmail),
		apiToken:     resolve(basic.APIToken, envAPIToken),
		clientID:     resolve(account.ClientID, envClientID),
		clientSecret: resolve(account.ClientSecret, envClientSecret),
		cloudID:      resolve(account.CloudID, envCloudID),
	}

	confluenceConfig, diags := credentials.confluenceConfig()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	atlassianClient, err := client.New(client.Config{
		AdminAPIKey: credentials.adminAPIKey.value,
		Confluence:  confluenceConfig,
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to configure Atlassian client", fmt.Sprintf("Invalid provider configuration: %s", err))
		return
	}

	resp.DataSourceData = atlassianClient
	resp.ResourceData = atlassianClient
}

func (p *AtlassianProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		organizationservice.NewGroupResource,
		organizationpolicyservice.NewPolicyResource,
		datasecuritypolicyservice.NewDataSecurityPolicyResource,
		organizationservice.NewGroupMembershipResource,
		organizationservice.NewGroupRoleAssignmentResource,
		organizationservice.NewUserOrganizationRoleAssignmentResource,
		organizationservice.NewUserRoleAssignmentResource,
	}
}

func (p *AtlassianProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		organizationservice.NewGroupDataSource,
		organizationpolicyservice.NewPolicyDataSource,
		organizationpolicyservice.NewPoliciesDataSource,
		organizationservice.NewGroupsDataSource,
		organizationservice.NewUserDataSource,
		organizationservice.NewUsersDataSource,
		organizationservice.NewWorkspacesDataSource,
	}
}

// resolvedValue is a configuration value together with where it came from, so
// a combination error can point at the right place. Environment variable
// names are shared with other Atlassian tooling, and the most likely way to
// configure two credential types at once is a block in configuration meeting
// a pair left in the shell.
type resolvedValue struct {
	value  string
	source string
}

func (v resolvedValue) set() bool { return strings.TrimSpace(v.value) != "" }

// resolve prefers a value written in configuration and falls back to the
// environment, mirroring the per-attribute behavior the provider has always
// had for admin_api_key.
func resolve(value types.String, environmentVariable string) resolvedValue {
	if !value.IsNull() && !value.IsUnknown() {
		return resolvedValue{value: value.ValueString(), source: "configuration"}
	}
	return resolvedValue{value: os.Getenv(environmentVariable), source: "environment variable " + environmentVariable}
}

type resolvedCredentials struct {
	adminAPIKey  resolvedValue
	siteURL      resolvedValue
	email        resolvedValue
	apiToken     resolvedValue
	clientID     resolvedValue
	clientSecret resolvedValue
	cloudID      resolvedValue
}

// confluenceConfig applies the presence and combination rules and returns the
// Confluence configuration, or nil when no Confluence credential is present.
// A stray cloud id alone does not count as a service account configuration.
func (c resolvedCredentials) confluenceConfig() (*confluence.Config, diag.Diagnostics) {
	var diags diag.Diagnostics
	basicPresent := c.email.set() || c.apiToken.set()
	accountPresent := c.clientID.set() || c.clientSecret.set()

	switch {
	case basicPresent && accountPresent:
		diags.AddError(
			"Conflicting Confluence credentials",
			fmt.Sprintf("Both basic_auth and service_account are configured; remove one. basic_auth.email came from %s, basic_auth.api_token from %s, service_account.client_id from %s, service_account.client_secret from %s.",
				describeSource(c.email), describeSource(c.apiToken), describeSource(c.clientID), describeSource(c.clientSecret)),
		)
		return nil, diags
	case basicPresent:
		diags.Append(requirePair(attrBasicAuth, "email", c.email, envEmail, "api_token", c.apiToken, envAPIToken)...)
		if !c.siteURL.set() {
			diags.AddError("Missing site_url", "basic_auth requires site_url (or "+envSiteURL+") so requests can be sent to the Confluence site.")
		}
		if diags.HasError() {
			return nil, diags
		}
		return &confluence.Config{
			Mode:     confluence.AuthBasic,
			SiteURL:  c.siteURL.value,
			Email:    c.email.value,
			APIToken: c.apiToken.value,
		}, diags
	case accountPresent:
		diags.Append(requirePair(attrServiceAccount, "client_id", c.clientID, envClientID, "client_secret", c.clientSecret, envClientSecret)...)
		if !c.cloudID.set() && !c.siteURL.set() {
			diags.AddError("Missing site identity", "service_account requires service_account.cloud_id (or "+envCloudID+"), or site_url (or "+envSiteURL+") to discover the cloud id from.")
		}
		if diags.HasError() {
			return nil, diags
		}
		return &confluence.Config{
			Mode:         confluence.AuthServiceAccount,
			SiteURL:      c.siteURL.value,
			ClientID:     c.clientID.value,
			ClientSecret: c.clientSecret.value,
			CloudID:      c.cloudID.value,
		}, diags
	default:
		return nil, diags
	}
}

// requirePair reports the missing half of a two-attribute credential, naming
// the environment variable that would also satisfy it.
func requirePair(block, firstName string, first resolvedValue, firstEnv, secondName string, second resolvedValue, secondEnv string) diag.Diagnostics {
	var diags diag.Diagnostics
	if first.set() && !second.set() {
		diags.AddError("Incomplete "+block+" credentials", fmt.Sprintf("%s.%s is set (from %s) but %s.%s is not. Set it in configuration or with %s.", block, firstName, first.source, block, secondName, secondEnv))
	}
	if second.set() && !first.set() {
		diags.AddError("Incomplete "+block+" credentials", fmt.Sprintf("%s.%s is set (from %s) but %s.%s is not. Set it in configuration or with %s.", block, secondName, second.source, block, firstName, firstEnv))
	}
	return diags
}

func describeSource(v resolvedValue) string {
	if !v.set() {
		return "nowhere (unset)"
	}
	return v.source
}

// objectAs decodes a nested attribute when it is known and non-null. Unknown
// and null objects are skipped: unknown is Terraform's to resolve, and null
// means the block was not written.
//
// Whether the decode succeeded is judged on diagnostics local to this decode,
// not on the shared collection: validation reports every problem it finds in
// one pass, so an unrelated earlier error (a malformed site_url, say) must not
// make this block look undecodable and silently skip its own checks.
func objectAs[T any](ctx context.Context, object types.Object, diags *diag.Diagnostics) (T, bool) {
	var target T
	if object.IsNull() || object.IsUnknown() {
		return target, false
	}
	decodeDiagnostics := object.As(ctx, &target, basetypes.ObjectAsOptions{})
	diags.Append(decodeDiagnostics...)
	return target, !decodeDiagnostics.HasError()
}

func knownString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func validateNonEmpty(attribute path.Path, value types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if knownString(value) && strings.TrimSpace(value.ValueString()) == "" {
		diags.AddAttributeError(attribute, "Empty value", attribute.String()+" must not be empty or whitespace. Omit the attribute to fall back to its environment variable.")
	}
	return diags
}

func validateSiteURL(attribute path.Path, raw string) diag.Diagnostics {
	var diags diag.Diagnostics
	parsed, err := confluence.ParseSiteURL(raw)
	if err != nil {
		diags.AddAttributeError(attribute, "Invalid site_url", err.Error())
		return diags
	}
	if parsed.Scheme == "http" {
		diags.AddAttributeWarning(attribute, "site_url uses plain HTTP", "Credentials will be sent unencrypted. Use https unless a local proxy or test rig requires http.")
	}
	return diags
}

// validateEmail catches an account id pasted where an email belongs, which
// the API would report only as an opaque 401.
func validateEmail(attribute path.Path, value string) diag.Diagnostics {
	var diags diag.Diagnostics
	local, domain, found := strings.Cut(value, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") || !strings.Contains(domain, ".") {
		diags.AddAttributeError(attribute, "Invalid email", "basic_auth.email must be the Atlassian account's email address, for example user@example.com.")
	}
	return diags
}

// cloudIDPattern matches the lowercase UUID that _edge/tenant_info returns.
// The gateway rejects anything else with an unrouted-path 404 that names
// neither the attribute nor the cause.
var cloudIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validateCloudID(attribute path.Path, value string) diag.Diagnostics {
	var diags diag.Diagnostics
	if !cloudIDPattern.MatchString(strings.TrimSpace(value)) {
		diags.AddAttributeError(attribute, "Invalid cloud_id", "service_account.cloud_id must be the site's cloud id, a lowercase UUID such as the one returned by https://<site>/_edge/tenant_info.")
	}
	return diags
}
