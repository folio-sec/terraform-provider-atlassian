package organization

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestUserDetailsDataSourceRead(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: userDetailsRoundTripFunc(func(request *http.Request) *http.Response {
		if request.Method != http.MethodGet || request.URL.Path != "/admin/v2/orgs/org/directories/directory/users/712020:account" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer admin-key" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"data":{"accountId":"712020:account","name":"Example User","email":"user@example.com","emailVerified":true,"status":"active","platformRoles":["atlassian/org-admin"]}}`,
			)),
			Request: request,
		}
	})}
	atlassianClient, err := client.New(client.Config{AdminAPIKey: "admin-key", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	subject := NewUserDataSource().(*userDetailsDataSource)
	subject.client = atlassianClient.Organization

	ctx := context.Background()
	var schemaResponse datasource.SchemaResponse
	subject.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	model := userDetailsDataSourceModel{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		AccountID:      types.StringValue("712020:account"),
		PlatformRoles:  types.SetNull(types.StringType),
	}
	configState := tfsdk.State{Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil), Schema: schemaResponse.Schema}
	if diagnostics := configState.Set(ctx, &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schemaResponse.Schema}
	state := tfsdk.State{Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil), Schema: schemaResponse.Schema}
	response := datasource.ReadResponse{State: state}
	subject.Read(ctx, datasource.ReadRequest{Config: config}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var result userDetailsDataSourceModel
	if diagnostics := response.State.Get(ctx, &result); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if result.ID.ValueString() != "712020:account" || result.Email.ValueString() != "user@example.com" || !result.EmailVerified.ValueBool() {
		t.Fatalf("result = %#v", result)
	}
	var roles []string
	if diagnostics := result.PlatformRoles.ElementsAs(ctx, &roles, false); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if len(roles) != 1 || roles[0] != "atlassian/org-admin" {
		t.Fatalf("platform roles = %#v", roles)
	}
}

type userDetailsRoundTripFunc func(*http.Request) *http.Response

func (f userDetailsRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request), nil
}
