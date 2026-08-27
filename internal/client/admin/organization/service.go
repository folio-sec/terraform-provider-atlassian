package organization

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
)

const pageLimit = 100

type apiClient interface {
	SearchDirectoryUsersWithResponse(context.Context, generated.OrgIdParam, generated.DirectoryIdParam, generated.SearchDirectoryUsersJSONRequestBody, ...generated.RequestEditorFn) (*generated.SearchDirectoryUsersResponse, error)
	SearchDirectoryGroupsWithResponse(context.Context, generated.OrgIdParam, generated.DirectoryIdParam, generated.SearchDirectoryGroupsJSONRequestBody, ...generated.RequestEditorFn) (*generated.SearchDirectoryGroupsResponse, error)
	CreateGroupWithResponse(context.Context, string, string, generated.CreateGroupJSONRequestBody, ...generated.RequestEditorFn) (*generated.CreateGroupResponse, error)
	GetGroupWithResponse(context.Context, generated.OrgIdParam, generated.DirectoryIdParam, generated.GroupIdParam, ...generated.RequestEditorFn) (*generated.GetGroupResponse, error)
	DeleteGroupWithResponse(context.Context, string, string, string, ...generated.RequestEditorFn) (*generated.DeleteGroupResponse, error)
	GetUserRoleAssignmentsWithResponse(context.Context, generated.OrgIdParam, generated.DirectoryIdParam, generated.AccountIdParam, *generated.GetUserRoleAssignmentsParams, ...generated.RequestEditorFn) (*generated.GetUserRoleAssignmentsResponse, error)
	AssignRoleWithResponse(context.Context, generated.OrgIdParam, string, generated.AssignRoleJSONRequestBody, ...generated.RequestEditorFn) (*generated.AssignRoleResponse, error)
	RevokeRoleWithResponse(context.Context, generated.OrgIdParam, string, generated.RevokeRoleJSONRequestBody, ...generated.RequestEditorFn) (*generated.RevokeRoleResponse, error)
	AssignOrganizationLevelRoleWithResponse(context.Context, string, string, generated.AssignOrganizationLevelRoleJSONRequestBody, ...generated.RequestEditorFn) (*generated.AssignOrganizationLevelRoleResponse, error)
	RevokeOrganizationLevelRoleWithResponse(context.Context, string, string, generated.RevokeOrganizationLevelRoleJSONRequestBody, ...generated.RequestEditorFn) (*generated.RevokeOrganizationLevelRoleResponse, error)
	AddUserToGroupWithResponse(context.Context, string, string, string, generated.AddUserToGroupJSONRequestBody, ...generated.RequestEditorFn) (*generated.AddUserToGroupResponse, error)
	RemoveUserFromGroupWithResponse(context.Context, string, string, string, string, ...generated.RequestEditorFn) (*generated.RemoveUserFromGroupResponse, error)
}

// Service implements Organization API behavior on top of the generated API
// client. Pagination, Terraform-facing types, and idempotency policy remain in
// this handwritten layer.
type Service struct {
	client apiClient
}

func NewService(client *admin.Client) (*Service, error) {
	generatedClient, err := generated.NewClientWithResponses(
		client.BaseURL("admin"),
		generated.WithHTTPClient(client.HTTPClient()),
		generated.WithRequestEditorFn(client.EditRequest),
	)
	if err != nil {
		return nil, fmt.Errorf("configure Organization API client: %w", err)
	}
	return &Service{client: generatedClient}, nil
}

type SearchUsersRequest struct {
	AccountIDs       []string
	DirectoryIDs     []string
	ResourceIDs      []string
	GroupIDs         []string
	MFAEnabled       *bool
	ClaimStatus      string
	Status           []string
	AccountStatus    []string
	MembershipStatus []string
	RoleIDs          []string
	EmailDomains     []string
	SearchTerm       string
	Emails           []string
}

type User struct {
	AccountID        string
	AccountType      *string
	Status           *string
	AccountStatus    *string
	MembershipStatus *string
	AddedToOrg       *string
	Name             *string
	Nickname         *string
	Email            *string
	EmailVerified    *bool
	ClaimStatus      *string
	Picture          *string
	Avatar           *string
	ManagementSource *string
	MFAEnabled       *bool
	JobTitle         *string
	Department       *string
	Organization     *string
	Location         *string
	TimeZone         *string
}

type SearchGroupsRequest struct {
	AccountIDs     []string
	DirectoryIDs   []string
	RoleIDs        []string
	ResourceOwners []string
	ResourceIDs    []string
	SearchTerm     string
	GroupIDs       []string
	GroupNames     []string
}

type Group struct {
	ID               string
	Name             *string
	Description      *string
	DirectoryID      *string
	ExternalSynced   *bool
	ManagedBy        *string
	ManagementAccess *ManagementAccess
}

type ManagementAccess struct {
	Deletable  *bool
	Modifiable *bool
	Readable   *bool
}

// CreateGroup creates a group. The endpoint deliberately has no response body,
// so callers must resolve the resulting ID separately.
func (s *Service) CreateGroup(ctx context.Context, organizationID, directoryID, name string, description *string) error {
	request := generated.CreateGroupJSONRequestBody{Name: name, Description: description}
	response, err := s.client.CreateGroupWithResponse(admin.WithoutRetry(ctx), organizationID, directoryID, request)
	if err != nil {
		return fmt.Errorf("create group: %w", err)
	}
	if response.StatusCode() != http.StatusCreated {
		return fmt.Errorf("create group: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// GetGroup returns group details by immutable Atlassian group ID.
func (s *Service) GetGroup(ctx context.Context, organizationID, directoryID, groupID string) (Group, error) {
	response, err := s.client.GetGroupWithResponse(ctx, organizationID, directoryID, groupID)
	if err != nil {
		return Group{}, fmt.Errorf("get group: %w", err)
	}
	if response.StatusCode() != http.StatusOK {
		return Group{}, fmt.Errorf("get group: %w", responseError(response.HTTPResponse, response.Body))
	}
	if response.JSON200 == nil || response.JSON200.Data == nil {
		return Group{}, fmt.Errorf("get group: API returned an invalid success response")
	}
	group := response.JSON200.Data
	if group.Id == nil || strings.TrimSpace(*group.Id) == "" {
		return Group{}, fmt.Errorf("get group: API returned a group without id")
	}
	return groupFromFields(*group.Id, group.Name, group.Description, group.DirectoryId, group.ExternalSynced, group.ManagedBy, group.ManagementAccess), nil
}

// DeleteGroup deletes a group by immutable Atlassian group ID.
func (s *Service) DeleteGroup(ctx context.Context, organizationID, directoryID, groupID string) error {
	response, err := s.client.DeleteGroupWithResponse(admin.WithoutRetry(ctx), organizationID, directoryID, groupID)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("delete group: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// SearchUsers follows all cursors and returns every user matching the supplied
// filters. Pagination controls are intentionally internal to the provider.
func (s *Service) SearchUsers(ctx context.Context, organizationID, directoryID string, filters SearchUsersRequest) ([]User, error) {
	request := searchRequest(filters)
	limit := int32(pageLimit)
	request.Limit = &limit
	request.Cursor = nil
	fetch := func(ctx context.Context, request generated.MultiDirectoryUserSearchRequest) (searchPage[generated.MultiDirectoryUser], error) {
		return s.searchUserPage(ctx, organizationID, directoryID, request)
	}
	return collectSearchPages(ctx, "search users", request, fetch, func(request *generated.MultiDirectoryUserSearchRequest, cursor string) {
		request.Cursor = &cursor
	}, userFromGenerated)
}

func (s *Service) searchUserPage(ctx context.Context, organizationID, directoryID string, request generated.MultiDirectoryUserSearchRequest) (searchPage[generated.MultiDirectoryUser], error) {
	response, err := s.client.SearchDirectoryUsersWithResponse(ctx, organizationID, directoryID, request)
	if err != nil {
		return searchPage[generated.MultiDirectoryUser]{}, fmt.Errorf("request user page: %w", err)
	}
	return checkedSearchPage(response.StatusCode(), response.HTTPResponse, response.Body, response.JSON200,
		func(page *generated.MultiDirectoryUserSearchPage) ([]generated.MultiDirectoryUser, *generated.LinkPageCursor) {
			return page.Data, page.Links
		})
}

// SearchGroups follows all cursors and returns every group matching the
// supplied filters. Pagination controls are intentionally internal to the
// provider.
func (s *Service) SearchGroups(ctx context.Context, organizationID, directoryID string, filters SearchGroupsRequest) ([]Group, error) {
	request := groupSearchRequest(filters)
	limit := int32(pageLimit)
	request.Limit = &limit
	request.Cursor = nil
	fetch := func(ctx context.Context, request generated.MultiDirectoryGroupSearchRequest) (searchPage[generated.MultiDirectoryGroup], error) {
		return s.searchGroupPage(ctx, organizationID, directoryID, request)
	}
	return collectSearchPages(ctx, "search groups", request, fetch, func(request *generated.MultiDirectoryGroupSearchRequest, cursor string) {
		request.Cursor = &cursor
	}, groupFromGenerated)
}

func (s *Service) searchGroupPage(ctx context.Context, organizationID, directoryID string, request generated.MultiDirectoryGroupSearchRequest) (searchPage[generated.MultiDirectoryGroup], error) {
	response, err := s.client.SearchDirectoryGroupsWithResponse(ctx, organizationID, directoryID, request)
	if err != nil {
		return searchPage[generated.MultiDirectoryGroup]{}, fmt.Errorf("request group page: %w", err)
	}
	return checkedSearchPage(response.StatusCode(), response.HTTPResponse, response.Body, response.JSON200,
		func(page *generated.MultiDirectoryGroupSearchPage) ([]generated.MultiDirectoryGroup, *generated.LinkPageCursor) {
			return page.Data, page.Links
		})
}

// HasGroupMembership reports whether the user belongs to the group in the
// specified directory.
func (s *Service) HasGroupMembership(ctx context.Context, organizationID, directoryID, groupID, accountID string) (bool, error) {
	users, err := s.SearchUsers(ctx, organizationID, directoryID, SearchUsersRequest{
		AccountIDs: []string{accountID},
		GroupIDs:   []string{groupID},
	})
	if err != nil {
		return false, fmt.Errorf("check group membership: %w", err)
	}
	for _, user := range users {
		if user.AccountID == accountID {
			return true, nil
		}
	}
	return false, nil
}

// AddGroupMembership adds a user to a group in the specified directory.
func (s *Service) AddGroupMembership(ctx context.Context, organizationID, directoryID, groupID, accountID string) error {
	request := generated.AddUserToGroupJSONRequestBody{AccountId: accountID}
	response, err := s.client.AddUserToGroupWithResponse(admin.WithoutRetry(ctx), organizationID, directoryID, groupID, request)
	if err != nil {
		return fmt.Errorf("add group membership: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("add group membership: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// RemoveGroupMembership removes a user from a group in the specified directory.
func (s *Service) RemoveGroupMembership(ctx context.Context, organizationID, directoryID, groupID, accountID string) error {
	response, err := s.client.RemoveUserFromGroupWithResponse(admin.WithoutRetry(ctx), organizationID, directoryID, groupID, accountID)
	if err != nil {
		return fmt.Errorf("remove group membership: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("remove group membership: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// AssignUserRole grants a platform role for a resource to a user.
func (s *Service) AssignUserRole(ctx context.Context, organizationID, accountID, resource, role string) error {
	request := generated.RoleApiRequest{Role: generated.RoleApiRequestRole(role)}
	if resource != "" {
		request.Resource = &resource
	}
	response, err := s.client.AssignRoleWithResponse(admin.WithoutRetry(ctx), organizationID, accountID, request)
	if err != nil {
		return fmt.Errorf("assign user role: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("assign user role: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// RevokeUserRole revokes a platform role for a resource from a user.
func (s *Service) RevokeUserRole(ctx context.Context, organizationID, accountID, resource, role string) error {
	request := generated.RoleApiRequest{Role: generated.RoleApiRequestRole(role)}
	if resource != "" {
		request.Resource = &resource
	}
	response, err := s.client.RevokeRoleWithResponse(admin.WithoutRetry(ctx), organizationID, accountID, request)
	if err != nil {
		return fmt.Errorf("revoke user role: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("revoke user role: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// AssignOrganizationRole grants an organization-level role directly to a user.
func (s *Service) AssignOrganizationRole(ctx context.Context, organizationID, accountID, role string) error {
	request := generated.OrganizationLevelRoleApiRequest{
		Role: generated.OrganizationLevelRoleApiRequestRole(role),
	}
	response, err := s.client.AssignOrganizationLevelRoleWithResponse(admin.WithoutRetry(ctx), organizationID, accountID, request)
	if err != nil {
		return fmt.Errorf("assign organization-level role: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("assign organization-level role: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// RevokeOrganizationRole revokes an organization-level role directly from a user.
func (s *Service) RevokeOrganizationRole(ctx context.Context, organizationID, accountID, role string) error {
	request := generated.OrganizationLevelRoleApiRequest{
		Role: generated.OrganizationLevelRoleApiRequestRole(role),
	}
	response, err := s.client.RevokeOrganizationLevelRoleWithResponse(admin.WithoutRetry(ctx), organizationID, accountID, request)
	if err != nil {
		return fmt.Errorf("revoke organization-level role: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("revoke organization-level role: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// HasDirectOrganizationRole reports whether an organization-level role is
// assigned directly to the user. Resource IDs are intentionally ignored
// because organization-level assignments are not resource scoped.
func (s *Service) HasDirectOrganizationRole(ctx context.Context, organizationID, directoryID, accountID, role string) (bool, error) {
	return s.hasDirectRole(ctx, "get organization-level role assignments", organizationID, directoryID, accountID, "", role)
}

// HasDirectUserRole reports whether the role is assigned directly to the user,
// excluding access inherited through a group or inferred by Atlassian.
func (s *Service) HasDirectUserRole(ctx context.Context, organizationID, directoryID, accountID, resource, role string) (bool, error) {
	return s.hasDirectRole(ctx, "get user role assignments", organizationID, directoryID, accountID, resource, role)
}

// hasDirectRole follows every page of a user's role assignments and reports
// whether the role is granted directly, excluding access inherited through a
// group or inferred by Atlassian. An empty resource matches assignments
// regardless of their scope, which organization-level roles require.
func (s *Service) hasDirectRole(ctx context.Context, operation, organizationID, directoryID, accountID, resource, role string) (bool, error) {
	limit := pageLimit
	roleIDs := []string{role}
	params := generated.GetUserRoleAssignmentsParams{
		Limit:   &limit,
		RoleIds: &roleIDs,
	}
	if resource != "" {
		resourceIDs := []string{resource}
		params.ResourceIds = &resourceIDs
	}
	seenCursors := map[string]struct{}{}
	for {
		response, err := s.client.GetUserRoleAssignmentsWithResponse(ctx, organizationID, directoryID, accountID, &params)
		if err != nil {
			return false, fmt.Errorf("%s: %w", operation, err)
		}
		if response.StatusCode() != http.StatusOK {
			return false, fmt.Errorf("%s: %w", operation, responseError(response.HTTPResponse, response.Body))
		}
		if response.JSON200 == nil {
			return false, fmt.Errorf("%s: API returned an invalid success response", operation)
		}
		if response.JSON200.Data != nil {
			for _, resourceAssignment := range *response.JSON200.Data {
				if resource != "" && (resourceAssignment.ResourceId == nil || *resourceAssignment.ResourceId != resource) {
					continue
				}
				if resourceAssignment.RoleAssignments == nil {
					continue
				}
				for _, assignment := range *resourceAssignment.RoleAssignments {
					if assignment.Role == nil || string(*assignment.Role) != role || assignment.RoleAssignmentMethods == nil {
						continue
					}
					for _, method := range *assignment.RoleAssignmentMethods {
						if method == generated.Direct {
							return true, nil
						}
					}
				}
			}
		}

		next := nextCursor(response.JSON200.Links)
		if next == "" {
			return false, nil
		}
		if _, exists := seenCursors[next]; exists {
			return false, fmt.Errorf("%s: API returned repeated pagination cursor %q", operation, next)
		}
		seenCursors[next] = struct{}{}
		params = generated.GetUserRoleAssignmentsParams{Cursor: &next}
	}
}

func searchRequest(filters SearchUsersRequest) generated.MultiDirectoryUserSearchRequest {
	request := generated.MultiDirectoryUserSearchRequest{
		MfaEnabled: filters.MFAEnabled,
	}
	setStrings := func(values []string, target **[]string) {
		if len(values) > 0 {
			copied := append([]string(nil), values...)
			*target = &copied
		}
	}
	setStrings(filters.AccountIDs, &request.AccountIds)
	setStrings(filters.DirectoryIDs, &request.DirectoryIds)
	setStrings(filters.ResourceIDs, &request.ResourceIds)
	setStrings(filters.GroupIDs, &request.GroupIds)
	setStrings(filters.EmailDomains, &request.EmailDomains)
	setStrings(filters.Emails, &request.Emails)
	if filters.SearchTerm != "" {
		request.SearchTerm = &filters.SearchTerm
	}
	if filters.ClaimStatus != "" {
		value := generated.MultiDirectoryUserSearchRequestClaimStatus(filters.ClaimStatus)
		request.ClaimStatus = &value
	}
	if len(filters.Status) > 0 {
		values := make([]generated.MultiDirectoryUserSearchRequestStatus, len(filters.Status))
		for i, value := range filters.Status {
			values[i] = generated.MultiDirectoryUserSearchRequestStatus(value)
		}
		request.Status = &values
	}
	if len(filters.AccountStatus) > 0 {
		values := make([]generated.MultiDirectoryUserSearchRequestAccountStatus, len(filters.AccountStatus))
		for i, value := range filters.AccountStatus {
			values[i] = generated.MultiDirectoryUserSearchRequestAccountStatus(value)
		}
		request.AccountStatus = &values
	}
	if len(filters.MembershipStatus) > 0 {
		values := make([]generated.MultiDirectoryUserSearchRequestMembershipStatus, len(filters.MembershipStatus))
		for i, value := range filters.MembershipStatus {
			values[i] = generated.MultiDirectoryUserSearchRequestMembershipStatus(value)
		}
		request.MembershipStatus = &values
	}
	if len(filters.RoleIDs) > 0 {
		values := make([]generated.MultiDirectoryUserSearchRequestRoleIds, len(filters.RoleIDs))
		for i, value := range filters.RoleIDs {
			values[i] = generated.MultiDirectoryUserSearchRequestRoleIds(value)
		}
		request.RoleIds = &values
	}
	return request
}

func groupSearchRequest(filters SearchGroupsRequest) generated.MultiDirectoryGroupSearchRequest {
	request := generated.MultiDirectoryGroupSearchRequest{}
	setStrings := func(values []string, target **[]string) {
		if len(values) > 0 {
			copied := append([]string(nil), values...)
			*target = &copied
		}
	}
	setStrings(filters.AccountIDs, &request.AccountIds)
	setStrings(filters.DirectoryIDs, &request.DirectoryIds)
	setStrings(filters.RoleIDs, &request.RoleIds)
	setStrings(filters.ResourceOwners, &request.ResourceOwners)
	setStrings(filters.ResourceIDs, &request.ResourceIds)
	setStrings(filters.GroupIDs, &request.GroupIds)
	setStrings(filters.GroupNames, &request.GroupNames)
	if filters.SearchTerm != "" {
		request.SearchTerm = &filters.SearchTerm
	}
	return request
}

func userFromGenerated(user generated.MultiDirectoryUser) (User, error) {
	if user.AccountId == nil || strings.TrimSpace(*user.AccountId) == "" {
		return User{}, fmt.Errorf("API returned a user without accountId")
	}
	managementSource := (*string)(nil)
	if source, err := user.ManagementSource.Get(); err == nil {
		value := string(source)
		managementSource = &value
	}
	return User{
		AccountID:        *user.AccountId,
		AccountType:      enumPointer(user.AccountType),
		Status:           enumPointer(user.Status),
		AccountStatus:    enumPointer(user.AccountStatus),
		MembershipStatus: enumPointer(user.MembershipStatus),
		AddedToOrg:       user.AddedToOrg,
		Name:             user.Name,
		Nickname:         user.Nickname,
		Email:            user.Email,
		EmailVerified:    user.EmailVerified,
		ClaimStatus:      enumPointer(user.ClaimStatus),
		Picture:          user.Picture,
		Avatar:           user.Avatar,
		ManagementSource: managementSource,
		MFAEnabled:       user.MfaEnabled,
		JobTitle:         user.JobTitle,
		Department:       user.Department,
		Organization:     user.Organization,
		Location:         user.Location,
		TimeZone:         user.TimeZone,
	}, nil
}

func groupFromGenerated(group generated.MultiDirectoryGroup) (Group, error) {
	if group.Id == nil || strings.TrimSpace(*group.Id) == "" {
		return Group{}, fmt.Errorf("API returned a group without id")
	}
	return groupFromFields(*group.Id, group.Name, group.Description, group.DirectoryId, group.ExternalSynced, group.ManagedBy, group.ManagementAccess), nil
}

func groupFromFields(id string, name, description, directoryID *string, externalSynced *bool, managedBy *string, access *generated.ManagementAccess) Group {
	var managementAccess *ManagementAccess
	if access != nil {
		managementAccess = &ManagementAccess{
			Deletable:  access.Deletable,
			Modifiable: access.Modifiable,
			Readable:   access.Readable,
		}
	}
	return Group{
		ID:               id,
		Name:             name,
		Description:      description,
		DirectoryID:      directoryID,
		ExternalSynced:   externalSynced,
		ManagedBy:        managedBy,
		ManagementAccess: managementAccess,
	}
}

func enumPointer[T ~string](value *T) *string {
	if value == nil {
		return nil
	}
	converted := string(*value)
	return &converted
}

func nextCursor(links *generated.LinkPageCursor) string {
	if links == nil || links.Next == nil {
		return ""
	}
	return *links.Next
}

type searchPage[T any] struct {
	data  []T
	links *generated.LinkPageCursor
}

func checkedSearchPage[Page, Result any](
	statusCode int,
	response *http.Response,
	body []byte,
	payload *Page,
	values func(*Page) ([]Result, *generated.LinkPageCursor),
) (searchPage[Result], error) {
	if statusCode != http.StatusOK {
		return searchPage[Result]{}, responseError(response, body)
	}
	if payload == nil {
		return searchPage[Result]{}, fmt.Errorf("API returned an invalid success response")
	}
	data, links := values(payload)
	return searchPage[Result]{data: data, links: links}, nil
}

func collectSearchPages[Request, APIResult, Result any](
	ctx context.Context,
	operation string,
	request Request,
	fetch func(context.Context, Request) (searchPage[APIResult], error),
	setCursor func(*Request, string),
	convert func(APIResult) (Result, error),
) ([]Result, error) {
	var results []Result
	seenCursors := map[string]struct{}{}
	for {
		page, err := fetch(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		for _, value := range page.data {
			converted, err := convert(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation, err)
			}
			results = append(results, converted)
		}

		next := nextCursor(page.links)
		if next == "" {
			return results, nil
		}
		if _, exists := seenCursors[next]; exists {
			return nil, fmt.Errorf("%s: API returned repeated pagination cursor %q", operation, next)
		}
		seenCursors[next] = struct{}{}
		setCursor(&request, next)
	}
}

func responseError(response *http.Response, body []byte) error {
	statusCode := 0
	method := ""
	requestURL := ""
	if response != nil {
		statusCode = response.StatusCode
		if response.Request != nil {
			method = response.Request.Method
			requestURL = response.Request.URL.String()
		}
	}
	return &admin.HTTPError{
		StatusCode: statusCode,
		Method:     method,
		URL:        requestURL,
		Body:       strings.TrimSpace(string(body)),
	}
}
