package space

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	v1gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v1/generated"
	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
)

// Principal identifies one subject on either Confluence space-access API.
type Principal struct {
	Type string
	ID   string
}

// PermissionOperation is the public API key/target pair for one Custom access
// permission. UI labels are localized and deliberately do not appear here.
type PermissionOperation struct {
	Key        string
	TargetType string
}

// PermissionAssignment is one legacy permission assignment returned by v2.
type PermissionAssignment struct {
	ID        string
	Principal Principal
	Operation PermissionOperation
}

// SpaceRole is one tenant-specific role catalogue entry.
type SpaceRole struct {
	ID            string
	Name          string
	Description   string
	Type          string
	PermissionIDs []string
}

// SpaceRoleAssignment maps one principal to one role in a space.
type SpaceRoleAssignment struct {
	Principal Principal
	RoleID    string
}

type generatedPage[T any] struct {
	statusCode int
	body       []byte
	results    *[]T
	links      *v2gen.MultiEntityLinks
}

// GetSpacePermissionsAssignments follows every page of the v2 permission
// read. A partial page sequence is always an error because absence drives
// destructive reconciliation in the resource layer.
func (s *Service) GetSpacePermissionsAssignments(ctx context.Context, spaceID string) ([]PermissionAssignment, error) {
	const operation = "get space permission assignments"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	limit := int32(pageLimit)
	params := &v2gen.GetSpacePermissionsAssignmentsParams{Limit: &limit}
	return collectGeneratedPages(operation, "/spaces/"+spaceID+"/permissions", func(cursor *string) (generatedPage[v2gen.SpacePermissionAssignment], error) {
		params.Cursor = cursor
		resp, err := v2.GetSpacePermissionsAssignmentsWithResponse(ctx, spaceID, params)
		if err != nil {
			return generatedPage[v2gen.SpacePermissionAssignment]{}, fmt.Errorf("request permission page: %w", err)
		}
		page := generatedPage[v2gen.SpacePermissionAssignment]{statusCode: resp.StatusCode(), body: resp.Body}
		if resp.JSON200 != nil {
			page.results = resp.JSON200.Results
			page.links = resp.JSON200.UnderscoreLinks
		}
		return page, nil
	}, permissionAssignmentFromGenerated)
}

// GetAvailableSpaceRoles reads the complete tenant role catalogue.
func (s *Service) GetAvailableSpaceRoles(ctx context.Context) ([]SpaceRole, error) {
	const operation = "get available space roles"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	limit := int32(pageLimit)
	params := &v2gen.GetAvailableSpaceRolesParams{Limit: &limit}
	return collectGeneratedPages(operation, "/space-roles", func(cursor *string) (generatedPage[v2gen.SpaceRole], error) {
		params.Cursor = cursor
		resp, err := v2.GetAvailableSpaceRolesWithResponse(ctx, params)
		if err != nil {
			return generatedPage[v2gen.SpaceRole]{}, fmt.Errorf("request role page: %w", err)
		}
		page := generatedPage[v2gen.SpaceRole]{statusCode: resp.StatusCode(), body: resp.Body}
		if resp.JSON200 != nil {
			page.results = resp.JSON200.Results
			page.links = resp.JSON200.UnderscoreLinks
		}
		return page, nil
	}, spaceRoleFromGenerated)
}

// GetSpaceRoleAssignments reads every matching role assignment. When
// principal is non-nil the API-side principal filters are always paired.
func (s *Service) GetSpaceRoleAssignments(ctx context.Context, spaceID string, principal *Principal) ([]SpaceRoleAssignment, error) {
	const operation = "get space role assignments"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	limit := int32(pageLimit)
	params := &v2gen.GetSpaceRoleAssignmentsParams{Limit: &limit}
	if principal != nil {
		principalType := v2gen.PrincipalType(principal.Type)
		params.PrincipalType = &principalType
		params.PrincipalId = &principal.ID
	}
	return collectGeneratedPages(operation, "/spaces/"+spaceID+"/role-assignments", func(cursor *string) (generatedPage[v2gen.SpaceRoleAssignment], error) {
		params.Cursor = cursor
		resp, err := v2.GetSpaceRoleAssignmentsWithResponse(ctx, spaceID, params)
		if err != nil {
			return generatedPage[v2gen.SpaceRoleAssignment]{}, fmt.Errorf("request role-assignment page: %w", err)
		}
		page := generatedPage[v2gen.SpaceRoleAssignment]{statusCode: resp.StatusCode(), body: resp.Body}
		if resp.JSON200 != nil {
			page.results = resp.JSON200.Results
			page.links = resp.JSON200.UnderscoreLinks
		}
		return page, nil
	}, roleAssignmentFromGenerated)
}

// SetSpaceRoleAssignment performs the measured entry-level upsert. A nil
// roleID removes only this principal's assignment.
func (s *Service) SetSpaceRoleAssignment(ctx context.Context, spaceID string, principal Principal, roleID *string) error {
	const operation = "set space role assignments"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
	}
	principalType := v2gen.PrincipalType(principal.Type)
	body := v2gen.SetSpaceRoleAssignmentsJSONRequestBody{{
		Principal: v2gen.Principal{PrincipalType: &principalType, PrincipalId: &principal.ID},
		RoleId:    roleID,
	}}
	resp, err := v2.SetSpaceRoleAssignmentsWithResponse(confluence.WithoutRetry(ctx), spaceID, body)
	if err != nil {
		if resp == nil && errors.Is(err, confluence.ErrAuthorize) {
			return fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
		}
		return fmt.Errorf("%s: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodPost, "/spaces/"+spaceID+"/role-assignments", resp.StatusCode(), resp.Body); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if resp.JSON200 == nil || resp.JSON200.Results == nil {
		return fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	return nil
}

// AddPermissionToSpace creates one legacy permission assignment. The server
// may create companion assignments, so callers must re-read the full set.
func (s *Service) AddPermissionToSpace(ctx context.Context, spaceKey string, principal Principal, operation PermissionOperation) (string, error) {
	const operationName = "add permission to space"
	v1, err := s.client.V1(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: %w: %w", operationName, ErrRequestNotSent, err)
	}
	body := v1gen.SpacePermissionRequest{
		Subject: v1gen.PermissionSubject{
			Type:       v1gen.PermissionSubjectType(principal.Type),
			Identifier: principal.ID,
		},
	}
	body.Operation.Key = v1gen.SpacePermissionRequestOperationKey(operation.Key)
	body.Operation.Target = v1gen.SpacePermissionRequestOperationTarget(operation.TargetType)
	resp, err := v1.AddPermissionToSpaceWithResponse(confluence.WithoutRetry(ctx), spaceKey, body)
	if err != nil {
		if resp == nil && errors.Is(err, confluence.ErrAuthorize) {
			return "", fmt.Errorf("%s: %w: %w", operationName, ErrRequestNotSent, err)
		}
		return "", fmt.Errorf("%s: %w", operationName, err)
	}
	if err := confluence.CheckResponse(http.MethodPost, "/wiki/rest/api/space/"+spaceKey+"/permission", resp.StatusCode(), resp.Body); err != nil {
		return "", fmt.Errorf("%s: %w", operationName, err)
	}
	if resp.JSON200 == nil {
		return "", fmt.Errorf("%s: API returned an invalid success response", operationName)
	}
	return strconv.FormatInt(resp.JSON200.Id, 10), nil
}

// RemovePermission removes one legacy assignment by the id returned from v2.
func (s *Service) RemovePermission(ctx context.Context, spaceKey, assignmentID string) error {
	const operation = "remove permission"
	id, err := strconv.Atoi(assignmentID)
	if err != nil || id < 1 {
		return fmt.Errorf("%s: %w: invalid permission id %q", operation, ErrRequestNotSent, assignmentID)
	}
	v1, err := s.client.V1(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
	}
	resp, err := v1.RemovePermissionWithResponse(confluence.WithoutRetry(ctx), spaceKey, id)
	if err != nil {
		if resp == nil && errors.Is(err, confluence.ErrAuthorize) {
			return fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
		}
		return fmt.Errorf("%s: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodDelete, "/wiki/rest/api/space/"+spaceKey+"/permission/"+assignmentID, resp.StatusCode(), resp.Body); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func permissionAssignmentFromGenerated(row v2gen.SpacePermissionAssignment) (PermissionAssignment, error) {
	if row.Id == nil || row.Principal == nil || row.Principal.Id == nil || row.Principal.Type == nil || row.Operation == nil || row.Operation.Key == nil || row.Operation.TargetType == nil {
		return PermissionAssignment{}, fmt.Errorf("permission assignment omitted a required field")
	}
	return PermissionAssignment{
		ID:        *row.Id,
		Principal: Principal{Type: string(*row.Principal.Type), ID: *row.Principal.Id},
		Operation: PermissionOperation{Key: string(*row.Operation.Key), TargetType: string(*row.Operation.TargetType)},
	}, nil
}

func spaceRoleFromGenerated(row v2gen.SpaceRole) (SpaceRole, error) {
	if row.Id == nil || row.Name == nil || row.Type == nil || row.SpacePermissions == nil {
		return SpaceRole{}, fmt.Errorf("space role omitted a required field")
	}
	description := ""
	if row.Description != nil {
		description = *row.Description
	}
	return SpaceRole{
		ID: *row.Id, Name: *row.Name, Description: description, Type: string(*row.Type),
		PermissionIDs: append([]string(nil), (*row.SpacePermissions)...),
	}, nil
}

func roleAssignmentFromGenerated(row v2gen.SpaceRoleAssignment) (SpaceRoleAssignment, error) {
	if row.Principal == nil || row.Principal.PrincipalType == nil || row.Principal.PrincipalId == nil || row.RoleId == nil {
		return SpaceRoleAssignment{}, fmt.Errorf("space role assignment omitted a required field")
	}
	return SpaceRoleAssignment{
		Principal: Principal{Type: string(*row.Principal.PrincipalType), ID: *row.Principal.PrincipalId},
		RoleID:    *row.RoleId,
	}, nil
}

func collectGeneratedPages[Generated, Model any](
	operation string,
	path string,
	fetch func(*string) (generatedPage[Generated], error),
	convert func(Generated) (Model, error),
) ([]Model, error) {
	var models []Model
	seen := map[string]struct{}{}
	var cursor *string
	for {
		page, err := fetch(cursor)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		if err := confluence.CheckResponse(http.MethodGet, path, page.statusCode, page.body); err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		if page.results == nil {
			return nil, fmt.Errorf("%s: API returned an invalid success response", operation)
		}
		for _, row := range *page.results {
			converted, err := convert(row)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation, err)
			}
			models = append(models, converted)
		}
		next, more, err := nextPageCursor(page.links, seen)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		if !more {
			return models, nil
		}
		cursor = &next
	}
}

func nextLink(links *v2gen.MultiEntityLinks) string {
	if links == nil || links.Next == nil {
		return ""
	}
	return *links.Next
}

func nextPageCursor(links *v2gen.MultiEntityLinks, seen map[string]struct{}) (string, bool, error) {
	next := nextLink(links)
	if next == "" {
		return "", false, nil
	}
	cursor, err := cursorFromNextLink(next)
	if err != nil {
		return "", false, err
	}
	if _, exists := seen[cursor]; exists {
		return "", false, fmt.Errorf("API returned repeated pagination cursor %q", cursor)
	}
	seen[cursor] = struct{}{}
	return cursor, true, nil
}
