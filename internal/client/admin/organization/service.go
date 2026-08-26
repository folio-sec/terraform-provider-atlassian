package organization

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
	"github.com/google/uuid"
)

const pageLimit = 100

type apiClient interface {
	SearchDirectoryUsersWithResponse(context.Context, generated.OrgIdParam, generated.DirectoryIdParam, generated.SearchDirectoryUsersJSONRequestBody, ...generated.RequestEditorFn) (*generated.SearchDirectoryUsersResponse, error)
	GetUserRoleAssignmentsWithResponse(context.Context, generated.OrgIdParam, generated.DirectoryIdParam, generated.AccountIdParam, *generated.GetUserRoleAssignmentsParams, ...generated.RequestEditorFn) (*generated.GetUserRoleAssignmentsResponse, error)
	AssignRoleWithResponse(context.Context, generated.OrgIdParam, uuid.UUID, generated.AssignRoleJSONRequestBody, ...generated.RequestEditorFn) (*generated.AssignRoleResponse, error)
	RevokeRoleWithResponse(context.Context, generated.OrgIdParam, uuid.UUID, generated.RevokeRoleJSONRequestBody, ...generated.RequestEditorFn) (*generated.RevokeRoleResponse, error)
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

// SearchUsers follows all cursors and returns every user matching the supplied
// filters. Pagination controls are intentionally internal to the provider.
func (s *Service) SearchUsers(ctx context.Context, organizationID, directoryID string, filters SearchUsersRequest) ([]User, error) {
	request := searchRequest(filters)
	limit := int32(pageLimit)
	request.Limit = &limit
	request.Cursor = nil

	var users []User
	seenCursors := map[string]struct{}{}
	for {
		response, err := s.client.SearchDirectoryUsersWithResponse(ctx, organizationID, directoryID, request)
		if err != nil {
			return nil, fmt.Errorf("search users: %w", err)
		}
		if response.StatusCode() != http.StatusOK {
			return nil, fmt.Errorf("search users: %w", responseError(response.HTTPResponse, response.Body))
		}
		if response.JSON200 == nil {
			return nil, fmt.Errorf("search users: API returned an invalid success response")
		}
		for _, user := range response.JSON200.Data {
			converted, err := userFromGenerated(user)
			if err != nil {
				return nil, fmt.Errorf("search users: %w", err)
			}
			users = append(users, converted)
		}

		next := nextCursor(response.JSON200.Links)
		if next == "" {
			return users, nil
		}
		if _, exists := seenCursors[next]; exists {
			return nil, fmt.Errorf("search users: API returned repeated pagination cursor %q", next)
		}
		seenCursors[next] = struct{}{}
		request.Cursor = &next
	}
}

// AssignUserRole grants a platform role for a resource to a user.
func (s *Service) AssignUserRole(ctx context.Context, organizationID, accountID, resource, role string) error {
	userID, err := uuid.Parse(accountID)
	if err != nil {
		return fmt.Errorf("assign user role: account ID must be a UUID: %w", err)
	}
	request := generated.RoleApiRequest{Role: generated.RoleApiRequestRole(role)}
	if resource != "" {
		request.Resource = &resource
	}
	response, err := s.client.AssignRoleWithResponse(admin.WithoutRetry(ctx), organizationID, userID, request)
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
	userID, err := uuid.Parse(accountID)
	if err != nil {
		return fmt.Errorf("revoke user role: account ID must be a UUID: %w", err)
	}
	request := generated.RoleApiRequest{Role: generated.RoleApiRequestRole(role)}
	if resource != "" {
		request.Resource = &resource
	}
	response, err := s.client.RevokeRoleWithResponse(admin.WithoutRetry(ctx), organizationID, userID, request)
	if err != nil {
		return fmt.Errorf("revoke user role: %w", err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("revoke user role: %w", responseError(response.HTTPResponse, response.Body))
	}
	return nil
}

// HasDirectUserRole reports whether the role is assigned directly to the user,
// excluding access inherited through a group or inferred by Atlassian.
func (s *Service) HasDirectUserRole(ctx context.Context, organizationID, directoryID, accountID, resource, role string) (bool, error) {
	limit := pageLimit
	resourceIDs := []string{resource}
	roleIDs := []string{role}
	params := generated.GetUserRoleAssignmentsParams{
		Limit:       &limit,
		ResourceIds: &resourceIDs,
		RoleIds:     &roleIDs,
	}
	seenCursors := map[string]struct{}{}
	for {
		response, err := s.client.GetUserRoleAssignmentsWithResponse(ctx, organizationID, directoryID, accountID, &params)
		if err != nil {
			return false, fmt.Errorf("get user role assignments: %w", err)
		}
		if response.StatusCode() != http.StatusOK {
			return false, fmt.Errorf("get user role assignments: %w", responseError(response.HTTPResponse, response.Body))
		}
		if response.JSON200 == nil {
			return false, fmt.Errorf("get user role assignments: API returned an invalid success response")
		}
		if response.JSON200.Data != nil {
			for _, resourceAssignment := range *response.JSON200.Data {
				if resourceAssignment.ResourceId == nil || *resourceAssignment.ResourceId != resource || resourceAssignment.RoleAssignments == nil {
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
			return false, fmt.Errorf("get user role assignments: API returned repeated pagination cursor %q", next)
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
