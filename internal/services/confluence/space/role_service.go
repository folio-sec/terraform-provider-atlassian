package space

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
)

// SpaceRoleWriteRequest is the desired definition of a tenant-wide custom
// space role. The reassignment ids are update-only API fields.
type SpaceRoleWriteRequest struct {
	Name                        string
	Description                 string
	PermissionIDs               []string
	AnonymousReassignmentRoleID *string
	GuestReassignmentRoleID     *string
}

// CreateSpaceRole creates a tenant-wide custom space role.
func (s *Service) CreateSpaceRole(ctx context.Context, req SpaceRoleWriteRequest) (SpaceRole, error) {
	const operation = "create space role"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return SpaceRole{}, fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
	}
	body := v2gen.CreateSpaceRoleJSONRequestBody{
		Name: req.Name, Description: req.Description, SpacePermissions: req.PermissionIDs,
	}
	resp, err := v2.CreateSpaceRoleWithResponse(confluence.WithoutRetry(ctx), body)
	if err != nil {
		if resp == nil && errors.Is(err, confluence.ErrAuthorize) {
			return SpaceRole{}, fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
		}
		return SpaceRole{}, fmt.Errorf("%s: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodPost, "/space-roles", resp.StatusCode(), resp.Body); err != nil {
		return SpaceRole{}, fmt.Errorf("%s: %w", operation, err)
	}
	if resp.JSON201 == nil {
		return SpaceRole{}, fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	role, err := spaceRoleFromGenerated(*resp.JSON201)
	if err != nil {
		partial := SpaceRole{}
		if resp.JSON201.Id != nil {
			partial.ID = *resp.JSON201.Id
		}
		if resp.JSON201.Type != nil {
			partial.Type = string(*resp.JSON201.Type)
		}
		return partial, fmt.Errorf("%s: %w", operation, err)
	}
	return role, nil
}

// GetSpaceRoleByID retrieves one tenant-wide space role.
func (s *Service) GetSpaceRoleByID(ctx context.Context, id string) (SpaceRole, error) {
	const operation = "get space role by id"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return SpaceRole{}, fmt.Errorf("%s: %w", operation, err)
	}
	resp, err := v2.GetSpaceRolesByIdWithResponse(ctx, id)
	if err != nil {
		return SpaceRole{}, fmt.Errorf("%s: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodGet, "/space-roles/"+id, resp.StatusCode(), resp.Body); err != nil {
		return SpaceRole{}, fmt.Errorf("%s: %w", operation, err)
	}
	if resp.JSON200 == nil {
		return SpaceRole{}, fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	row := v2gen.SpaceRole{
		Id: resp.JSON200.Id, Name: resp.JSON200.Name, Description: resp.JSON200.Description,
		Type: resp.JSON200.Type, SpacePermissions: resp.JSON200.SpacePermissions,
	}
	role, err := spaceRoleFromGenerated(row)
	if err != nil {
		return SpaceRole{}, fmt.Errorf("%s: %w", operation, err)
	}
	return role, nil
}

// UpdateSpaceRole replaces the writable definition of a tenant-wide role.
// Atlassian applies the change asynchronously; the resource layer waits for
// the returned task and then confirms the readable role definition.
func (s *Service) UpdateSpaceRole(ctx context.Context, id string, req SpaceRoleWriteRequest) (string, error) {
	const operation = "update space role"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
	}
	body := v2gen.UpdateSpaceRoleJSONRequestBody{
		Name: req.Name, Description: req.Description, SpacePermissions: req.PermissionIDs,
		AnonymousReassignmentRoleId: req.AnonymousReassignmentRoleID,
		GuestReassignmentRoleId:     req.GuestReassignmentRoleID,
	}
	resp, err := v2.UpdateSpaceRoleWithResponse(confluence.WithoutRetry(ctx), id, body)
	if err != nil {
		if resp == nil && errors.Is(err, confluence.ErrAuthorize) {
			return "", fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
		}
		return "", fmt.Errorf("%s: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodPut, "/space-roles/"+id, resp.StatusCode(), resp.Body); err != nil {
		return "", fmt.Errorf("%s: %w", operation, err)
	}
	if resp.JSON202 == nil {
		return "", fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	if resp.JSON202.TaskId == nil || *resp.JSON202.TaskId == "" {
		return "", fmt.Errorf("%s: API returned a success response without a task id", operation)
	}
	return *resp.JSON202.TaskId, nil
}

// DeleteSpaceRole requests deletion of a tenant-wide role and returns the
// long-task id Confluence assigns to the asynchronous operation. The caller
// verifies a Confluence 404 against the complete role catalogue, because the
// operation documents the same status for missing permission.
func (s *Service) DeleteSpaceRole(ctx context.Context, id string) (string, error) {
	const operation = "delete space role"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
	}
	resp, err := v2.DeleteSpaceRoleWithResponse(confluence.WithoutRetry(ctx), id)
	if err != nil {
		if resp == nil && errors.Is(err, confluence.ErrAuthorize) {
			return "", fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
		}
		return "", fmt.Errorf("%s: %w", operation, err)
	}
	if responseErr := confluence.CheckResponse(http.MethodDelete, "/space-roles/"+id, resp.StatusCode(), resp.Body); responseErr != nil {
		return "", fmt.Errorf("%s: %w", operation, responseErr)
	}
	if resp.JSON202 == nil {
		return "", fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	if resp.JSON202.TaskId == nil || *resp.JSON202.TaskId == "" {
		return "", fmt.Errorf("%s: API returned a success response without a task id", operation)
	}
	return *resp.JSON202.TaskId, nil
}
