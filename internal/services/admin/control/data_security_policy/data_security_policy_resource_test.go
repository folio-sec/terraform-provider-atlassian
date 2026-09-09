package datasecuritypolicy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/control/generated"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakePolicyClient struct {
	get    func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error)
	create func(context.Context, string, generated.CreateDataSecurityPolicyJSONRequestBody) (generated.ModelsDataSecurityPolicy, error) //nolint:staticcheck // No public replacement exists.
	update func(context.Context, string, string, generated.UpdateDataSecurityPolicyJSONRequestBody) error                               //nolint:staticcheck // No public replacement exists.
	delete func(context.Context, string, string) error
}

func (f *fakePolicyClient) GetDataSecurityPolicy(ctx context.Context, organizationID, policyID string) (generated.ModelsDataSecurityPolicy, error) {
	return f.get(ctx, organizationID, policyID)
}

func (f *fakePolicyClient) CreateDataSecurityPolicy(ctx context.Context, organizationID string, body generated.CreateDataSecurityPolicyJSONRequestBody) (generated.ModelsDataSecurityPolicy, error) { //nolint:staticcheck // No public replacement exists.
	return f.create(ctx, organizationID, body)
}

func (f *fakePolicyClient) UpdateDataSecurityPolicy(ctx context.Context, organizationID, policyID string, body generated.UpdateDataSecurityPolicyJSONRequestBody) error { //nolint:staticcheck // No public replacement exists.
	return f.update(ctx, organizationID, policyID, body)
}

func (f *fakePolicyClient) DeleteDataSecurityPolicy(ctx context.Context, organizationID, policyID string) error {
	return f.delete(ctx, organizationID, policyID)
}

func testPolicy(t *testing.T, status string) generated.ModelsDataSecurityPolicy {
	t.Helper()
	body := `{"id":"draft-id","type":"policy","attributes":{"type":"data-security","name":"draft","status":"` + status + `","rule":{"export":{"effect":"allow"}},"metadata":{"policyCoverageLevel":"ORG","description":"temporary"}}}`
	var policy generated.ModelsDataSecurityPolicy
	if err := json.Unmarshal([]byte(body), &policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func testState(t *testing.T, subject *dataSecurityPolicyResource) (tfsdk.State, *tfsdk.ResourceIdentity) {
	t.Helper()
	ctx := context.Background()
	var schemaResponse resource.SchemaResponse
	subject.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	var identityResponse resource.IdentitySchemaResponse
	subject.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &identityResponse)
	state := tfsdk.State{
		Schema: schemaResponse.Schema,
		Raw:    tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil),
	}
	model := resourceModel{
		OrganizationID: types.StringValue("org"),
		CreateTimeout:  types.StringValue("10ms"),
		UpdateTimeout:  types.StringValue("10ms"),
		DeleteTimeout:  types.StringValue("10ms"),
	}
	var diagnostics diag.Diagnostics
	setState(&model, "draft-id", desiredFixture(), &diagnostics)
	diagnostics.Append(state.Set(ctx, &model)...)
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	identity := &tfsdk.ResourceIdentity{
		Schema: identityResponse.IdentitySchema,
		Raw:    tftypes.NewValue(identityResponse.IdentitySchema.Type().TerraformType(ctx), nil),
	}
	return state, identity
}

func desiredFixture() desiredPolicy {
	return desiredPolicy{Name: "draft", Status: "draft", Effect: "allow", Coverage: "ORG", Description: "temporary"}
}

func TestDataSecurityPolicyValidationAllowsUnknownNestedObjects(t *testing.T) {
	t.Parallel()

	dataAttributeTypes := dataTypes()
	attributesTypes := dataAttributeTypes["attributes"].(types.ObjectType).AttrTypes
	ruleTypes := attributesTypes["rule"].(types.ObjectType).AttrTypes
	exportTypes := ruleTypes["export"].(types.ObjectType).AttrTypes
	metadataTypes := attributesTypes["metadata"].(types.ObjectType).AttrTypes
	export := types.ObjectValueMust(exportTypes, map[string]attr.Value{
		"effect": types.StringValue("allow"),
	})
	rule := types.ObjectValueMust(ruleTypes, map[string]attr.Value{
		"export": export,
	})
	metadata := types.ObjectValueMust(metadataTypes, map[string]attr.Value{
		"policy_coverage_level": types.StringValue("ORG"),
		"description":           types.StringValue("temporary"),
	})

	for name, nested := range map[string]struct {
		rule     types.Object
		metadata types.Object
	}{
		"rule":     {rule: types.ObjectUnknown(ruleTypes), metadata: metadata},
		"metadata": {rule: rule, metadata: types.ObjectUnknown(metadataTypes)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			attributes := types.ObjectValueMust(attributesTypes, map[string]attr.Value{
				"type":     types.StringValue("data-security"),
				"name":     types.StringValue("draft"),
				"status":   types.StringValue("draft"),
				"rule":     nested.rule,
				"metadata": nested.metadata,
			})
			data := types.ObjectValueMust(dataAttributeTypes, map[string]attr.Value{
				"id":         types.StringNull(),
				"type":       types.StringValue("policy"),
				"attributes": attributes,
			})
			_, diagnostics := planValues(context.Background(), resourceModel{Data: data}, true)
			if diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
		})
	}
}

func TestDataSecurityPolicyRequestModels(t *testing.T) {
	t.Parallel()

	create, err := createRequest(desiredFixture())
	if err != nil {
		t.Fatal(err)
	}
	update, err := updateRequest("draft-id", desiredFixture())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]any{"create": create, "update": update} {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatal(err)
		}
		data := document["data"].(map[string]any)
		attributes := data["attributes"].(map[string]any)
		if attributes["type"] != "data-security" || attributes["status"] != "draft" ||
			attributes["rule"].(map[string]any)["export"].(map[string]any)["effect"] != "allow" ||
			attributes["metadata"].(map[string]any)["policyCoverageLevel"] != "ORG" {
			t.Fatalf("%s request = %s", name, encoded)
		}
	}
}

func TestDataSecurityPolicyReadDeletedRemovesState(t *testing.T) {
	t.Parallel()

	for name, get := range map[string]func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error){
		"deleted status": func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			return testPolicy(t, "deleted"), nil
		},
		"404": func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			return generated.ModelsDataSecurityPolicy{}, &admin.HTTPError{StatusCode: 404}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			subject := &dataSecurityPolicyResource{client: &fakePolicyClient{get: get}}
			state, identity := testState(t, subject)
			response := resource.ReadResponse{State: state, Identity: identity}
			subject.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatalf("state = %v, diagnostics = %v", response.State, response.Diagnostics)
			}
		})
	}
}

func TestDataSecurityPolicyReadPreservesStateOnInvalidDeletedResponse(t *testing.T) {
	t.Parallel()

	subject := &dataSecurityPolicyResource{client: &fakePolicyClient{
		get: func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			return testPolicy(t, "deleted"), errors.New("response ID does not match requested ID")
		},
	}}
	state, identity := testState(t, subject)
	response := resource.ReadResponse{State: state, Identity: identity}
	subject.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("state = %v, diagnostics = %v", response.State, response.Diagnostics)
	}
}

func TestDataSecurityPolicyCreateWaitsWithoutReplay(t *testing.T) {
	t.Parallel()

	creates, reads := 0, 0
	policy := testPolicy(t, "draft")
	client := &fakePolicyClient{
		create: func(context.Context, string, generated.CreateDataSecurityPolicyJSONRequestBody) (generated.ModelsDataSecurityPolicy, error) { //nolint:staticcheck // No public replacement exists.
			creates++
			return policy, nil
		},
		get: func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			reads++
			if reads == 1 {
				return generated.ModelsDataSecurityPolicy{}, &admin.HTTPError{StatusCode: 404}
			}
			return policy, nil
		},
	}
	subject := &dataSecurityPolicyResource{client: client, pollInterval: time.Microsecond}
	state, identity := testState(t, subject)
	response := resource.CreateResponse{
		State:    tfsdk.State{Schema: state.Schema, Raw: tftypes.NewValue(state.Raw.Type(), nil)},
		Identity: identity,
	}
	subject.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan(state)}, &response)
	if response.Diagnostics.HasError() || creates != 1 || reads != 2 || !response.State.Raw.IsFullyKnown() {
		t.Fatalf("creates = %d, reads = %d, diagnostics = %v", creates, reads, response.Diagnostics)
	}
}

func TestDataSecurityPolicyDeleteCompletesOnDeletedStatus(t *testing.T) {
	t.Parallel()

	reads, deletes := 0, 0
	client := &fakePolicyClient{
		get: func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			reads++
			if reads > 1 {
				return testPolicy(t, "deleted"), nil
			}
			return testPolicy(t, "draft"), nil
		},
		delete: func(context.Context, string, string) error {
			deletes++
			return nil
		},
	}
	subject := &dataSecurityPolicyResource{client: client, pollInterval: time.Microsecond}
	state, _ := testState(t, subject)
	var response resource.DeleteResponse
	subject.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() || deletes != 1 || reads != 2 {
		t.Fatalf("deletes = %d, reads = %d, diagnostics = %v", deletes, reads, response.Diagnostics)
	}
}

func TestDataSecurityPolicyDeleteRejectsInvalidDeletedResponse(t *testing.T) {
	t.Parallel()

	deletes := 0
	subject := &dataSecurityPolicyResource{client: &fakePolicyClient{
		get: func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			return testPolicy(t, "deleted"), errors.New("response ID does not match requested ID")
		},
		delete: func(context.Context, string, string) error {
			deletes++
			return nil
		},
	}}
	state, _ := testState(t, subject)
	var response resource.DeleteResponse
	subject.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || deletes != 0 {
		t.Fatalf("deletes = %d, diagnostics = %v", deletes, response.Diagnostics)
	}
}

func TestDataSecurityPolicyImportRejectsActivePolicy(t *testing.T) {
	t.Parallel()

	subject := &dataSecurityPolicyResource{client: &fakePolicyClient{
		get: func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			return testPolicy(t, "active"), nil
		},
	}}
	state, identity := testState(t, subject)
	state.Raw = tftypes.NewValue(state.Raw.Type(), nil)
	response := resource.ImportStateResponse{State: state, Identity: identity}
	subject.ImportState(context.Background(), resource.ImportStateRequest{ID: "org,draft-id"}, &response)
	if !response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
		t.Fatalf("state = %v, diagnostics = %v", response.State, response.Diagnostics)
	}
}

func TestDataSecurityPolicyAmbiguousUpdateUsesRead(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, "draft")
	reads, updates := 0, 0
	subject := &dataSecurityPolicyResource{pollInterval: time.Microsecond, client: &fakePolicyClient{
		get: func(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error) {
			reads++
			return policy, nil
		},
		update: func(context.Context, string, string, generated.UpdateDataSecurityPolicyJSONRequestBody) error { //nolint:staticcheck // No public replacement exists.
			updates++
			return errors.New("connection closed after write")
		},
	}}
	state, identity := testState(t, subject)
	response := resource.UpdateResponse{State: state, Identity: identity}
	subject.Update(context.Background(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(state)}, &response)
	if response.Diagnostics.HasError() || updates != 1 || reads != 2 {
		t.Fatalf("updates = %d, reads = %d, diagnostics = %v", updates, reads, response.Diagnostics)
	}
}
