package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakePolicyClient struct {
	get    func(context.Context, string, string) (generated.PolicyModel, error)
	create func(context.Context, string, generated.CreatePolicyJSONRequestBody) (generated.PolicyModel, error)
	update func(context.Context, string, string, generated.UpdatePolicyJSONRequestBody) error
	delete func(context.Context, string, string) error
}

func (f *fakePolicyClient) GetPolicyById(ctx context.Context, org, id string) (generated.PolicyModel, error) {
	return f.get(ctx, org, id)
}
func (f *fakePolicyClient) GetPolicies(context.Context, string, *string) ([]generated.PolicyModel, error) {
	panic("unexpected list")
}
func (f *fakePolicyClient) CreatePolicy(ctx context.Context, org string, b generated.CreatePolicyJSONRequestBody) (generated.PolicyModel, error) {
	return f.create(ctx, org, b)
}
func (f *fakePolicyClient) UpdatePolicy(ctx context.Context, org, id string, b generated.UpdatePolicyJSONRequestBody) error {
	return f.update(ctx, org, id, b)
}
func (f *fakePolicyClient) DeletePolicy(ctx context.Context, org, id string) error {
	return f.delete(ctx, org, id)
}

func testPolicy(t *testing.T) generated.PolicyModel {
	t.Helper()
	var p generated.PolicyModel
	if err := json.Unmarshal([]byte(`{"id":"id","type":"policy","attributes":{"type":"data-residency","name":"test","status":"disabled","rule":{"in":["jp"]},"resources":[]}}`), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func policyTestState(t *testing.T, r *policyResource) (tfsdk.State, *tfsdk.ResourceIdentity) {
	t.Helper()
	ctx := context.Background()
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	var i resource.IdentitySchemaResponse
	r.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &i)
	state := tfsdk.State{Schema: s.Schema, Raw: tftypes.NewValue(s.Schema.Type().TerraformType(ctx), nil)}
	model := policyResourceModel{OrganizationID: types.StringValue("org"), CreateTimeout: types.StringValue("10ms"), UpdateTimeout: types.StringValue("10ms"), DeleteTimeout: types.StringValue("10ms")}
	var ds diag.Diagnostics
	setPolicyResourceData(&model, "id", policyDesired{Name: "test", Status: "disabled", Realms: []string{"jp"}}, &ds)
	ds.Append(state.Set(ctx, &model)...)
	if ds.HasError() {
		t.Fatal(ds)
	}
	return state, &tfsdk.ResourceIdentity{Schema: i.IdentitySchema, Raw: tftypes.NewValue(i.IdentitySchema.Type().TerraformType(ctx), nil)}
}

func TestPolicyCreateConvergenceAndPartialState(t *testing.T) {
	for _, scenario := range []string{"delayed", "timeout", "forbidden", "cancelled", "partial response"} {
		t.Run(scenario, func(t *testing.T) {
			p := testPolicy(t)
			creates, reads := 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakePolicyClient{
				create: func(context.Context, string, generated.CreatePolicyJSONRequestBody) (generated.PolicyModel, error) {
					creates++
					if scenario == "cancelled" {
						cancel()
					}
					if scenario == "partial response" {
						return p, errors.New("missing type")
					}
					return p, nil
				},
				get: func(context.Context, string, string) (generated.PolicyModel, error) {
					reads++
					if scenario == "forbidden" {
						return generated.PolicyModel{}, &admin.HTTPError{StatusCode: 403}
					}
					if scenario == "delayed" && reads > 1 {
						return p, nil
					}
					return generated.PolicyModel{}, &admin.HTTPError{StatusCode: 404}
				},
			}
			r := &policyResource{client: f, pollInterval: time.Microsecond}
			state, identity := policyTestState(t, r)
			resp := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema, Raw: tftypes.NewValue(state.Raw.Type(), nil)}, Identity: identity}
			r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan(state)}, &resp)
			if resp.Diagnostics.HasError() != (scenario != "delayed") {
				t.Fatalf("diagnostics=%v", resp.Diagnostics)
			}
			var result policyResourceModel
			if ds := resp.State.Get(context.Background(), &result); ds.HasError() {
				t.Fatal(ds)
			}
			if creates != 1 || result.PolicyID.ValueString() != "id" || !resp.State.Raw.IsFullyKnown() {
				t.Fatalf("create calls=%d state=%v", creates, resp.State)
			}
		})
	}
}

func TestPolicyReadAbsenceAndErrors(t *testing.T) {
	for _, status := range []int{404, 401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			r := &policyResource{client: &fakePolicyClient{get: func(context.Context, string, string) (generated.PolicyModel, error) {
				return generated.PolicyModel{}, &admin.HTTPError{StatusCode: status}
			}}}
			state, identity := policyTestState(t, r)
			resp := resource.ReadResponse{State: state, Identity: identity}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.State.Raw.IsNull() != (status == 404) || resp.Diagnostics.HasError() != (status != 404) {
				t.Fatalf("status=%d state=%v diagnostics=%v", status, resp.State, resp.Diagnostics)
			}
		})
	}
}

func TestPolicyDeleteOutcomes(t *testing.T) {
	for _, scenario := range []string{"accepted", "delete404", "already absent", "ambiguous", "forbidden", "timeout", "unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			p := testPolicy(t)
			reads, deletes := 0, 0
			if scenario == "unsupported" {
				p.Attributes.Type = "generative-ai"
			}
			f := &fakePolicyClient{
				get: func(context.Context, string, string) (generated.PolicyModel, error) {
					reads++
					if scenario == "already absent" || (reads > 2 && scenario != "timeout") {
						return generated.PolicyModel{}, &admin.HTTPError{StatusCode: 404}
					}
					return p, nil
				},
				delete: func(context.Context, string, string) error {
					deletes++
					switch scenario {
					case "delete404":
						return &admin.HTTPError{StatusCode: 404}
					case "ambiguous":
						return &admin.HTTPError{StatusCode: 503}
					case "forbidden":
						return &admin.HTTPError{StatusCode: 403}
					}
					return nil
				},
			}
			r := &policyResource{client: f, pollInterval: time.Microsecond}
			state, _ := policyTestState(t, r)
			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			wantError := scenario == "forbidden" || scenario == "timeout" || scenario == "unsupported"
			if resp.Diagnostics.HasError() != wantError {
				t.Fatalf("diagnostics=%v", resp.Diagnostics)
			}
			wantDeletes := 1
			if scenario == "already absent" || scenario == "unsupported" {
				wantDeletes = 0
			}
			if deletes != wantDeletes {
				t.Fatalf("deletes=%d want=%d", deletes, wantDeletes)
			}
		})
	}
}

func TestPolicyImportCapabilities(t *testing.T) {
	for _, scenario := range []string{"supported", "type", "rule", "associations", "missing associations"} {
		for _, byIdentity := range []bool{false, true} {
			t.Run(scenario+map[bool]string{true: " identity", false: " string"}[byIdentity], func(t *testing.T) {
				p := testPolicy(t)
				switch scenario {
				case "type":
					p.Attributes.Type = "hipaa"
				case "rule":
					raw := json.RawMessage(`{"in":["jp"],"unknown":true}`)
					p.Attributes.Rule = &raw
				case "associations":
					rs := []generated.Resource{{Id: "site"}}
					p.Attributes.Resources = &rs
				case "missing associations":
					p.Attributes.Resources = nil
				}
				r := &policyResource{client: &fakePolicyClient{get: func(context.Context, string, string) (generated.PolicyModel, error) { return p, nil }}}
				state, identity := policyTestState(t, r)
				state.Raw = tftypes.NewValue(state.Raw.Type(), nil)
				req := resource.ImportStateRequest{ID: "org,id"}
				if byIdentity {
					if ds := identity.Set(context.Background(), policyIdentity{types.StringValue("org"), types.StringValue("id")}); ds.HasError() {
						t.Fatal(ds)
					}
					req = resource.ImportStateRequest{Identity: identity}
				}
				resp := resource.ImportStateResponse{State: state, Identity: identity}
				r.ImportState(context.Background(), req, &resp)
				if resp.Diagnostics.HasError() != (scenario != "supported") || resp.State.Raw.IsNull() != (scenario != "supported") {
					t.Fatalf("state=%v diagnostics=%v", resp.State, resp.Diagnostics)
				}
			})
		}
	}
}

func TestPolicyUpdateAmbiguousOutcome(t *testing.T) {
	p := testPolicy(t)
	reads, updates := 0, 0
	r := &policyResource{pollInterval: time.Microsecond, client: &fakePolicyClient{
		get: func(context.Context, string, string) (generated.PolicyModel, error) {
			reads++
			if reads > 1 {
				name := "updated"
				p.Attributes.Name = &name
			}
			return p, nil
		},
		update: func(_ context.Context, _ string, _ string, body generated.UpdatePolicyJSONRequestBody) error {
			updates++
			if *body.Data.Attributes.Name != "updated" {
				t.Fatal("wrong update body")
			}
			return errors.New("connection closed after write")
		},
	}}
	state, identity := policyTestState(t, r)
	planState, _ := policyTestState(t, r)
	var model policyResourceModel
	if ds := planState.Get(context.Background(), &model); ds.HasError() {
		t.Fatal(ds)
	}
	var ds diag.Diagnostics
	setPolicyResourceData(&model, "id", policyDesired{Name: "updated", Status: "disabled", Realms: []string{"jp"}}, &ds)
	ds.Append(planState.Set(context.Background(), &model)...)
	if ds.HasError() {
		t.Fatal(ds)
	}
	resp := resource.UpdateResponse{State: state, Identity: identity}
	r.Update(context.Background(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(planState)}, &resp)
	if resp.Diagnostics.HasError() || updates != 1 || !resp.State.Raw.Equal(planState.Raw) {
		t.Fatalf("updates=%d diagnostics=%v", updates, resp.Diagnostics)
	}
}

func TestPolicyJSONShapeAndPrecision(t *testing.T) {
	for input, want := range map[string]string{`[]`: `[]`, `{"z":9007199254740993,"a":[2,1]}`: `{"a":[2,1],"z":9007199254740993}`} {
		raw := json.RawMessage(input)
		var ds diag.Diagnostics
		if got := policyJSONValue(&raw, &ds); ds.HasError() || got.ValueString() != want {
			t.Fatalf("got=%v diagnostics=%v", got, ds)
		}
	}
	p := testPolicy(t)
	p.Attributes.Type = "hipaa"
	raw := json.RawMessage(`[]`)
	p.Attributes.Rule = &raw
	var ds diag.Diagnostics
	v := policyReadValue(p, &ds)
	if ds.HasError() || v.IsNull() {
		t.Fatal(ds)
	}
}
