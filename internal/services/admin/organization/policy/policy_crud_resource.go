package policy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func policyTimeout(value types.String) (time.Duration, error) {
	if value.IsNull() {
		return 2 * time.Minute, nil
	}
	duration, err := time.ParseDuration(value.ValueString())
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %w", err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return duration, nil
}

func policyOperationContext(ctx context.Context, value types.String, diagnostics *diag.Diagnostics) (context.Context, context.CancelFunc) {
	duration, err := policyTimeout(value)
	if err != nil {
		diagnostics.AddError("Invalid policy timeout", err.Error())
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, duration)
}

func (r *policyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model policyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	desired, ds := policyPlanValues(ctx, model, false)
	resp.Diagnostics.Append(ds...)
	opCtx, cancel := policyOperationContext(ctx, model.CreateTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	request, err := policyCreateRequest(desired)
	if err != nil {
		resp.Diagnostics.AddError("Unable to encode policy", err.Error())
		return
	}
	policy, err := r.client.CreatePolicy(opCtx, model.OrganizationID.ValueString(), request)
	if strings.TrimSpace(policy.Id) == "" {
		resp.Diagnostics.AddError("Unable to create policy", fmt.Sprintf("No policy ID was returned. The create request is not replayed; if it was accepted, locate the policy and import it. Cause: %v", err))
		return
	}
	// Save a complete, known partial state before waiting or reporting response errors.
	setPolicyResourceData(&model, policy.Id, desired, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, policyIdentity{model.OrganizationID, model.PolicyID})...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to verify created policy", err.Error())
		return
	}
	if _, err = supportedPolicy(policy); err != nil {
		resp.Diagnostics.AddError("Unsupported created policy", err.Error())
		return
	}
	if err = r.waitPolicy(opCtx, model.OrganizationID.ValueString(), policy.Id, &desired); err != nil {
		resp.Diagnostics.AddError("Unable to verify created policy", err.Error())
	}
}

func (r *policyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model policyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.GetPolicyById(ctx, model.OrganizationID.ValueString(), model.PolicyID.ValueString())
	if policyNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read policy", err.Error())
		return
	}
	desired, err := supportedPolicy(policy)
	if err != nil {
		resp.Diagnostics.AddError("Unsupported managed policy", err.Error())
		return
	}
	setPolicyResourceData(&model, policy.Id, desired, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, policyIdentity{model.OrganizationID, model.PolicyID})...)
	}
}

func (r *policyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model, prior policyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	desired, ds := policyPlanValues(ctx, model, false)
	resp.Diagnostics.Append(ds...)
	opCtx, cancel := policyOperationContext(ctx, model.UpdateTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	id := prior.PolicyID.ValueString()
	if _, err := r.checkManagedPolicy(opCtx, prior.OrganizationID.ValueString(), id); err != nil {
		resp.Diagnostics.AddError("Unable to update policy", err.Error())
		return
	}
	request, err := policyUpdateRequest(id, desired)
	if err != nil {
		resp.Diagnostics.AddError("Unable to encode policy", err.Error())
		return
	}
	updateErr := r.client.UpdatePolicy(opCtx, model.OrganizationID.ValueString(), id, request)
	if updateErr != nil && !mutationOutcomeMayBeAmbiguous(updateErr) {
		resp.Diagnostics.AddError("Unable to update policy", updateErr.Error())
		return
	}
	if err = r.waitPolicy(opCtx, model.OrganizationID.ValueString(), id, &desired); err != nil {
		resp.Diagnostics.AddError("Unable to verify policy update", fmt.Sprintf("%s; update response: %v", err, updateErr))
		return
	}
	setPolicyResourceData(&model, id, desired, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, policyIdentity{model.OrganizationID, model.PolicyID})...)
}

func (r *policyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model policyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	opCtx, cancel := policyOperationContext(ctx, model.DeleteTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	org, id := model.OrganizationID.ValueString(), model.PolicyID.ValueString()
	_, err := r.checkManagedPolicy(opCtx, org, id)
	if policyNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete policy", err.Error())
		return
	}
	deleteErr := r.client.DeletePolicy(opCtx, org, id)
	if policyNotFound(deleteErr) {
		return
	}
	if deleteErr != nil && !mutationOutcomeMayBeAmbiguous(deleteErr) {
		resp.Diagnostics.AddError("Unable to delete policy", deleteErr.Error())
		return
	}
	if err = r.waitPolicy(opCtx, org, id, nil); err != nil {
		resp.Diagnostics.AddError("Unable to verify policy deletion", fmt.Sprintf("%s; delete response: %v", err, deleteErr))
	}
}

func (r *policyResource) checkManagedPolicy(ctx context.Context, org, id string) (generated.PolicyModel, error) {
	policy, err := r.client.GetPolicyById(ctx, org, id)
	if err != nil {
		return policy, fmt.Errorf("read managed policy: %w", err)
	}
	_, err = supportedPolicy(policy)
	return policy, err
}

func policyNotFound(err error) bool {
	var httpErr *admin.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

func (r *policyResource) waitPolicy(ctx context.Context, org, id string, desired *policyDesired) error {
	interval := r.pollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	for {
		policy, err := r.client.GetPolicyById(ctx, org, id)
		switch {
		case policyNotFound(err):
			if desired == nil {
				return nil
			}
			// A create/update waiter must not invoke Read's state-removal behavior.
		case err != nil:
			if !readOutcomeMayBeTransient(err) {
				return fmt.Errorf("read policy during convergence: %w", err)
			}
		case desired != nil:
			actual, checkErr := supportedPolicy(policy)
			if checkErr != nil {
				return checkErr
			}
			if policyDesiredMatches(actual, *desired) {
				return nil
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("policy convergence stopped: %w", ctx.Err())
		case <-timer.C:
		}
		if interval < 3*time.Second {
			interval = min(interval*2, 3*time.Second)
		}
	}
}

func (r *policyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var identity policyIdentity
	switch {
	case req.ID != "":
		org, id, err := parsePolicyImportID(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid import identity", err.Error())
			return
		}
		identity = policyIdentity{types.StringValue(org), types.StringValue(id)}
	case req.Identity != nil:
		resp.Diagnostics.Append(req.Identity.Get(ctx, &identity)...)
	default:
		resp.Diagnostics.AddError("Invalid import identity", "Provide organization_id,policy_id or a resource identity.")
		return
	}
	for name, value := range map[string]types.String{"organization_id": identity.OrganizationID, "policy_id": identity.PolicyID} {
		if value.IsUnknown() || value.IsNull() || strings.TrimSpace(value.ValueString()) == "" {
			resp.Diagnostics.AddError("Invalid import identity", name+" must be known and nonblank.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.checkManagedPolicy(ctx, identity.OrganizationID.ValueString(), identity.PolicyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to import policy", err.Error())
		return
	}
	desired, err := supportedPolicy(policy)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import policy", err.Error())
		return
	}
	model := policyResourceModel{OrganizationID: identity.OrganizationID, CreateTimeout: types.StringValue("2m"), UpdateTimeout: types.StringValue("2m"), DeleteTimeout: types.StringValue("2m")}
	setPolicyResourceData(&model, policy.Id, desired, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, identity)...)
}
