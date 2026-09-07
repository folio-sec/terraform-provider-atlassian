package datasecuritypolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/control/generated"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func planValues(ctx context.Context, model resourceModel, allowUnknown bool) (desiredPolicy, diag.Diagnostics) {
	var desired desiredPolicy
	var diagnostics diag.Diagnostics
	if model.Data.IsUnknown() && allowUnknown {
		return desired, diagnostics
	}
	if model.Data.IsNull() || model.Data.IsUnknown() {
		diagnostics.AddError("Invalid data-security policy", "data must be known and non-null.")
		return desired, diagnostics
	}
	var data dataModel
	diagnostics.Append(model.Data.As(ctx, &data, basetypes.ObjectAsOptions{})...)
	if !data.Type.IsNull() && !data.Type.IsUnknown() && data.Type.ValueString() != "policy" {
		diagnostics.AddError("Invalid data-security policy", "data.type must be policy.")
	}
	if data.Attributes.IsUnknown() && allowUnknown {
		return desired, diagnostics
	}
	if data.Attributes.IsNull() || data.Attributes.IsUnknown() {
		diagnostics.AddError("Invalid data-security policy", "data.attributes must be known and non-null.")
		return desired, diagnostics
	}
	var attributes attributesModel
	diagnostics.Append(data.Attributes.As(ctx, &attributes, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(validateAttributes(attributes, allowUnknown)...)
	if diagnostics.HasError() {
		return desired, diagnostics
	}
	return nestedPlanValues(ctx, attributes, allowUnknown)
}

func nestedPlanValues(ctx context.Context, attributes attributesModel, allowUnknown bool) (desiredPolicy, diag.Diagnostics) {
	var desired desiredPolicy
	var diagnostics diag.Diagnostics
	var rule ruleModel
	var metadata metadataModel
	diagnostics.Append(attributes.Rule.As(ctx, &rule, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(attributes.Metadata.As(ctx, &metadata, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return desired, diagnostics
	}
	for name, value := range map[string]types.String{
		"metadata.policy_coverage_level": metadata.PolicyCoverageLevel,
		"metadata.description":           metadata.Description,
	} {
		if value.IsNull() || (!allowUnknown && value.IsUnknown()) {
			diagnostics.AddError("Invalid data-security policy", name+" must be known and non-null.")
		}
	}
	if !metadata.PolicyCoverageLevel.IsUnknown() && metadata.PolicyCoverageLevel.ValueString() != "ORG" {
		diagnostics.AddError("Unsupported data-security coverage", "Only ORG policy coverage has a verified lifecycle.")
	}
	if rule.Export.IsUnknown() && allowUnknown {
		return desired, diagnostics
	}
	if rule.Export.IsNull() || rule.Export.IsUnknown() {
		diagnostics.AddError("Invalid data-security policy", "rule.export must be known and non-null.")
		return desired, diagnostics
	}
	var effect effectModel
	diagnostics.Append(rule.Export.As(ctx, &effect, basetypes.ObjectAsOptions{})...)
	desired = desiredPolicy{
		Name:        attributes.Name.ValueString(),
		Status:      attributes.Status.ValueString(),
		Effect:      effect.Effect.ValueString(),
		Coverage:    metadata.PolicyCoverageLevel.ValueString(),
		Description: metadata.Description.ValueString(),
	}
	if !effect.Effect.IsUnknown() && desired.Effect != "allow" && desired.Effect != "block" {
		diagnostics.AddError("Unsupported data-security rule", "rule.export.effect must be allow or block.")
	}
	if !allowUnknown && effect.Effect.IsUnknown() {
		diagnostics.AddError("Invalid data-security policy", "rule.export.effect must be known before a mutation.")
	}
	return desired, diagnostics
}

func validateAttributes(attributes attributesModel, allowUnknown bool) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for name, value := range map[string]types.String{
		"attributes.type":   attributes.Type,
		"attributes.name":   attributes.Name,
		"attributes.status": attributes.Status,
	} {
		if value.IsNull() || (!allowUnknown && value.IsUnknown()) {
			diagnostics.AddError("Invalid data-security policy", name+" must be known and non-null.")
		}
	}
	if !attributes.Type.IsUnknown() && attributes.Type.ValueString() != "data-security" {
		diagnostics.AddError("Unsupported policy type", "data.attributes.type must be data-security.")
	}
	if !attributes.Status.IsUnknown() && attributes.Status.ValueString() != "draft" {
		diagnostics.AddError("Unsupported data-security status", "Only unpublished draft policies can be managed. Publishing is a separate bulk operation.")
	}
	if attributes.Rule.IsNull() || (!allowUnknown && attributes.Rule.IsUnknown()) {
		diagnostics.AddError("Invalid data-security policy", "rule must be known and non-null.")
	}
	if attributes.Metadata.IsNull() || (!allowUnknown && attributes.Metadata.IsUnknown()) {
		diagnostics.AddError("Invalid data-security policy", "metadata must be known and non-null.")
	}
	return diagnostics
}

func parseTimeout(value types.String) (time.Duration, error) {
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

func operationContext(ctx context.Context, value types.String, diagnostics *diag.Diagnostics) (context.Context, context.CancelFunc) {
	duration, err := parseTimeout(value)
	if err != nil {
		diagnostics.AddError("Invalid data-security policy timeout", err.Error())
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, duration)
}

func (r *dataSecurityPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	desired, diagnostics := planValues(ctx, model, false)
	resp.Diagnostics.Append(diagnostics...)
	opCtx, cancel := operationContext(ctx, model.CreateTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	request, err := createRequest(desired)
	if err != nil {
		resp.Diagnostics.AddError("Unable to encode data-security policy", err.Error())
		return
	}
	policy, err := r.client.CreateDataSecurityPolicy(opCtx, model.OrganizationID.ValueString(), request)
	if strings.TrimSpace(policy.Id) == "" {
		resp.Diagnostics.AddError("Unable to create data-security policy", fmt.Sprintf("No policy ID was returned. The create request is not replayed; locate and import any accepted draft. Cause: %v", err))
		return
	}
	setState(&model, policy.Id, desired, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, identityModel{model.OrganizationID, model.PolicyID})...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to verify created data-security policy", err.Error())
		return
	}
	if err := r.waitForPolicy(opCtx, model.OrganizationID.ValueString(), policy.Id, &desired); err != nil {
		resp.Diagnostics.AddError("Unable to verify created data-security policy", err.Error())
	}
}

func (r *dataSecurityPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.GetDataSecurityPolicy(ctx, model.OrganizationID.ValueString(), model.PolicyID.ValueString())
	if notFound(err) || policy.Attributes.Status != nil && *policy.Attributes.Status == "deleted" {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read data-security policy", err.Error())
		return
	}
	desired, err := supportedPolicy(policy)
	if err != nil {
		resp.Diagnostics.AddError("Unsupported managed data-security policy", err.Error())
		return
	}
	setState(&model, policy.Id, desired, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, identityModel{model.OrganizationID, model.PolicyID})...)
	}
}

func (r *dataSecurityPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model, prior resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	desired, diagnostics := planValues(ctx, model, false)
	resp.Diagnostics.Append(diagnostics...)
	opCtx, cancel := operationContext(ctx, model.UpdateTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	organizationID, policyID := prior.OrganizationID.ValueString(), prior.PolicyID.ValueString()
	if _, err := r.readSupported(opCtx, organizationID, policyID); err != nil {
		resp.Diagnostics.AddError("Unable to update data-security policy", err.Error())
		return
	}
	request, err := updateRequest(policyID, desired)
	if err != nil {
		resp.Diagnostics.AddError("Unable to encode data-security policy", err.Error())
		return
	}
	updateErr := r.client.UpdateDataSecurityPolicy(opCtx, organizationID, policyID, request)
	if updateErr != nil && !ambiguousMutation(updateErr) {
		resp.Diagnostics.AddError("Unable to update data-security policy", updateErr.Error())
		return
	}
	if err := r.waitForPolicy(opCtx, organizationID, policyID, &desired); err != nil {
		resp.Diagnostics.AddError("Unable to verify data-security policy update", fmt.Sprintf("%s; update response: %v", err, updateErr))
		return
	}
	setState(&model, policyID, desired, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, identityModel{model.OrganizationID, model.PolicyID})...)
}

func (r *dataSecurityPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	opCtx, cancel := operationContext(ctx, model.DeleteTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	organizationID, policyID := model.OrganizationID.ValueString(), model.PolicyID.ValueString()
	policy, err := r.client.GetDataSecurityPolicy(opCtx, organizationID, policyID)
	if notFound(err) || policy.Attributes.Status != nil && *policy.Attributes.Status == "deleted" {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete data-security policy", err.Error())
		return
	}
	if _, err := supportedPolicy(policy); err != nil {
		resp.Diagnostics.AddError("Unable to delete data-security policy", err.Error())
		return
	}
	deleteErr := r.client.DeleteDataSecurityPolicy(opCtx, organizationID, policyID)
	if notFound(deleteErr) {
		return
	}
	if deleteErr != nil && !ambiguousMutation(deleteErr) {
		resp.Diagnostics.AddError("Unable to delete data-security policy", deleteErr.Error())
		return
	}
	if err := r.waitForPolicy(opCtx, organizationID, policyID, nil); err != nil {
		resp.Diagnostics.AddError("Unable to verify data-security policy deletion", fmt.Sprintf("%s; delete response: %v", err, deleteErr))
	}
}

func (r *dataSecurityPolicyResource) readSupported(ctx context.Context, organizationID, policyID string) (desiredPolicy, error) {
	policy, err := r.client.GetDataSecurityPolicy(ctx, organizationID, policyID)
	if err != nil {
		return desiredPolicy{}, fmt.Errorf("read managed data-security policy: %w", err)
	}
	return supportedPolicy(policy)
}

func (r *dataSecurityPolicyResource) waitForPolicy(ctx context.Context, organizationID, policyID string, desired *desiredPolicy) error {
	interval := r.pollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	for {
		policy, err := r.client.GetDataSecurityPolicy(ctx, organizationID, policyID)
		switch {
		case notFound(err):
			if desired == nil {
				return nil
			}
		case err != nil:
			if !transientRead(err) {
				return fmt.Errorf("read data-security policy during convergence: %w", err)
			}
		case policy.Attributes.Status != nil && *policy.Attributes.Status == "deleted":
			if desired == nil {
				return nil
			}
			return fmt.Errorf("data-security policy was deleted during convergence")
		case desired != nil:
			actual, checkErr := supportedPolicy(policy)
			if checkErr != nil {
				return checkErr
			}
			if actual == *desired {
				return nil
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("data-security policy convergence stopped: %w", ctx.Err())
		case <-timer.C:
		}
		if interval < 3*time.Second {
			interval = min(interval*2, 3*time.Second)
		}
	}
}

func notFound(err error) bool {
	var httpErr *admin.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

func ambiguousMutation(err error) bool {
	var httpErr *admin.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= http.StatusInternalServerError
	}
	return true
}

func transientRead(err error) bool {
	var httpErr *admin.HTTPError
	if !errors.As(err, &httpErr) {
		return true
	}
	return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= http.StatusInternalServerError
}

func (r *dataSecurityPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	identity, diagnostics := importIdentity(ctx, req)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.GetDataSecurityPolicy(ctx, identity.OrganizationID.ValueString(), identity.PolicyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to import data-security policy", err.Error())
		return
	}
	desired, err := supportedPolicy(policy)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import data-security policy", err.Error())
		return
	}
	model := resourceModel{
		OrganizationID: identity.OrganizationID,
		CreateTimeout:  types.StringValue(defaultTimeout),
		UpdateTimeout:  types.StringValue(defaultTimeout),
		DeleteTimeout:  types.StringValue(defaultTimeout),
	}
	setState(&model, policy.Id, desired, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, identity)...)
}

func importIdentity(ctx context.Context, req resource.ImportStateRequest) (identityModel, diag.Diagnostics) {
	var identity identityModel
	var diagnostics diag.Diagnostics
	switch {
	case req.ID != "":
		parts := strings.Split(req.ID, ",")
		if len(parts) != 2 {
			diagnostics.AddError("Invalid import identity", "Expected organization_id,policy_id.")
			return identity, diagnostics
		}
		identity = identityModel{types.StringValue(parts[0]), types.StringValue(parts[1])}
	case req.Identity != nil:
		diagnostics.Append(req.Identity.Get(ctx, &identity)...)
	default:
		diagnostics.AddError("Invalid import identity", "Provide organization_id,policy_id or a resource identity.")
		return identity, diagnostics
	}
	for name, value := range map[string]types.String{"organization_id": identity.OrganizationID, "policy_id": identity.PolicyID} {
		if value.IsNull() || value.IsUnknown() || strings.TrimSpace(value.ValueString()) == "" {
			diagnostics.AddError("Invalid import identity", name+" must be known and nonblank.")
		}
	}
	return identity, diagnostics
}

func createRequest(desired desiredPolicy) (generated.CreateDataSecurityPolicyJSONRequestBody, error) { //nolint:staticcheck // No public replacement exists.
	var model generated.ModelsDataSecurityCreatePolicy
	if err := decodeDesired(desired, "", &model); err != nil {
		return generated.CreateDataSecurityPolicyJSONRequestBody{}, err //nolint:staticcheck // No public replacement exists.
	}
	var union generated.CreatePolicyV2
	if err := union.FromModelsDataSecurityCreatePolicy(model); err != nil {
		return generated.CreateDataSecurityPolicyJSONRequestBody{}, fmt.Errorf("encode data-security create union: %w", err) //nolint:staticcheck // No public replacement exists.
	}
	return generated.CreateDataSecurityPolicyJSONRequestBody{Data: union}, nil //nolint:staticcheck // No public replacement exists.
}

func updateRequest(policyID string, desired desiredPolicy) (generated.UpdateDataSecurityPolicyJSONRequestBody, error) { //nolint:staticcheck // No public replacement exists.
	var model generated.ModelsDataSecurityPolicy
	if err := decodeDesired(desired, policyID, &model); err != nil {
		return generated.UpdateDataSecurityPolicyJSONRequestBody{}, err //nolint:staticcheck // No public replacement exists.
	}
	var union generated.Policy
	if err := union.FromModelsDataSecurityPolicy(model); err != nil {
		return generated.UpdateDataSecurityPolicyJSONRequestBody{}, fmt.Errorf("encode data-security update union: %w", err) //nolint:staticcheck // No public replacement exists.
	}
	return generated.UpdateDataSecurityPolicyJSONRequestBody{Data: union}, nil //nolint:staticcheck // No public replacement exists.
}

func decodeDesired(desired desiredPolicy, policyID string, target any) error {
	data := map[string]any{
		"type": "policy",
		"attributes": map[string]any{
			"type":   "data-security",
			"name":   desired.Name,
			"status": desired.Status,
			"rule": map[string]any{
				"export": map[string]any{"effect": desired.Effect},
			},
			"metadata": map[string]any{
				"policyCoverageLevel": desired.Coverage,
				"description":         desired.Description,
			},
		},
	}
	if policyID != "" {
		data["id"] = policyID
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode generated data-security policy model: %w", err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("decode generated data-security policy model: %w", err)
	}
	return nil
}

func supportedPolicy(policy generated.ModelsDataSecurityPolicy) (desiredPolicy, error) {
	attributes := policy.Attributes
	if policy.Type != "policy" || attributes.Type != "data-security" || attributes.Name == nil || attributes.Status == nil || attributes.Rule == nil || attributes.Rule.Export == nil || attributes.Rule.Export.Effect == nil || attributes.Metadata == nil {
		return desiredPolicy{}, fmt.Errorf("policy has an unsupported data-security shape")
	}
	if *attributes.Status != "draft" {
		return desiredPolicy{}, fmt.Errorf("policy status %q is not a manageable draft", *attributes.Status)
	}
	if attributes.Metadata.PolicyCoverageLevel != "ORG" {
		return desiredPolicy{}, fmt.Errorf("policy coverage %q is not the verified ORG coverage", attributes.Metadata.PolicyCoverageLevel)
	}
	if *attributes.Rule.Export.Effect != "allow" && *attributes.Rule.Export.Effect != "block" {
		return desiredPolicy{}, fmt.Errorf("policy export effect %q is unsupported", *attributes.Rule.Export.Effect)
	}
	description := ""
	if attributes.Metadata.Description != nil {
		description = *attributes.Metadata.Description
	}
	return desiredPolicy{
		Name:        *attributes.Name,
		Status:      *attributes.Status,
		Effect:      *attributes.Rule.Export.Effect,
		Coverage:    string(attributes.Metadata.PolicyCoverageLevel),
		Description: description,
	}, nil
}

func dataTypes() map[string]attr.Type {
	return policyDataSchema().GetType().(types.ObjectType).AttrTypes
}

func setState(model *resourceModel, policyID string, desired desiredPolicy, diagnostics *diag.Diagnostics) {
	dataAttributeTypes := dataTypes()
	attributesTypes := dataAttributeTypes["attributes"].(types.ObjectType).AttrTypes
	ruleTypes := attributesTypes["rule"].(types.ObjectType).AttrTypes
	exportTypes := ruleTypes["export"].(types.ObjectType).AttrTypes
	metadataTypes := attributesTypes["metadata"].(types.ObjectType).AttrTypes

	export, current := types.ObjectValue(exportTypes, map[string]attr.Value{
		"effect": types.StringValue(desired.Effect),
	})
	diagnostics.Append(current...)
	rule, current := types.ObjectValue(ruleTypes, map[string]attr.Value{"export": export})
	diagnostics.Append(current...)
	metadata, current := types.ObjectValue(metadataTypes, map[string]attr.Value{
		"policy_coverage_level": types.StringValue(desired.Coverage),
		"description":           types.StringValue(desired.Description),
	})
	diagnostics.Append(current...)
	attributes, current := types.ObjectValue(attributesTypes, map[string]attr.Value{
		"type":     types.StringValue("data-security"),
		"name":     types.StringValue(desired.Name),
		"status":   types.StringValue(desired.Status),
		"rule":     rule,
		"metadata": metadata,
	})
	diagnostics.Append(current...)
	model.Data, current = types.ObjectValue(dataAttributeTypes, map[string]attr.Value{
		"id":         types.StringValue(policyID),
		"type":       types.StringValue("policy"),
		"attributes": attributes,
	})
	diagnostics.Append(current...)
	model.PolicyID = types.StringValue(policyID)
}
