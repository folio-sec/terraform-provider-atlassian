package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/control/generated"
)

// Service implements the data-security policy part of the Admin Control API.
// It is separate from the Organization API because its schema and draft
// lifecycle differ even though both APIs use the same Admin API credential.
type Service struct {
	client generated.ClientInterface
}

func NewService(client *admin.Client) (*Service, error) {
	generatedClient, err := generated.NewClientWithResponses(
		client.BaseURL(""),
		generated.WithHTTPClient(client.HTTPClient()),
		generated.WithRequestEditorFn(client.EditRequest),
	)
	if err != nil {
		return nil, fmt.Errorf("configure Admin Control API client: %w", err)
	}
	return &Service{client: generatedClient.ClientInterface}, nil
}

func responseBody(response *http.Response, requestErr error, expected int) ([]byte, error) {
	if requestErr != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, fmt.Errorf("data-security policy request: %w", requestErr)
	}
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("data-security policy request returned no response")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read data-security policy response: %w", err)
	}
	if response.StatusCode != expected {
		return nil, &admin.HTTPError{
			StatusCode: response.StatusCode,
			Method:     response.Request.Method,
			URL:        response.Request.URL.String(),
			Body:       strings.TrimSpace(string(body)),
		}
	}
	return body, nil
}

func decodePolicy(body []byte, expectedID string) (generated.ModelsDataSecurityPolicy, error) {
	var response generated.ModelsPolicyResponseV2
	if err := json.Unmarshal(body, &response); err != nil {
		return generated.ModelsDataSecurityPolicy{}, fmt.Errorf("decode data-security policy: %w", err)
	}
	policy, err := response.Data.AsModelsDataSecurityPolicy()
	if err != nil {
		return policy, fmt.Errorf("decode data-security policy data: %w", err)
	}
	if err := validatePolicy(policy, expectedID); err != nil {
		return policy, err
	}
	return policy, nil
}

func validatePolicy(policy generated.ModelsDataSecurityPolicy, expectedID string) error {
	if strings.TrimSpace(policy.Id) == "" || policy.Type != "policy" || policy.Attributes.Type != "data-security" {
		return fmt.Errorf("data-security policy response is missing identity or type")
	}
	if expectedID != "" && policy.Id != expectedID {
		return fmt.Errorf("data-security policy response ID does not match requested ID")
	}
	return nil
}

func (s *Service) GetDataSecurityPolicy(ctx context.Context, organizationID, policyID string) (generated.ModelsDataSecurityPolicy, error) {
	response, err := s.client.GetDataSecurityPolicy(ctx, organizationID, policyID) //nolint:staticcheck // No public replacement exists.
	body, err := responseBody(response, err, http.StatusOK)
	if err != nil {
		return generated.ModelsDataSecurityPolicy{}, err
	}
	return decodePolicy(body, policyID)
}

func (s *Service) GetDataSecurityPolicies(ctx context.Context, organizationID string) ([]generated.ModelsDataSecurityPolicy, error) {
	policyType := generated.ModelsPolicyType("data-security")
	params := &generated.GetDataSecurityPoliciesParams{Type: &policyType}
	result := make([]generated.ModelsDataSecurityPolicy, 0)
	seenCursors := map[string]bool{}
	seenIDs := map[string]bool{}
	for {
		response, err := s.client.GetDataSecurityPolicies(ctx, organizationID, params) //nolint:staticcheck // No public replacement exists.
		body, err := responseBody(response, err, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var page generated.ModelsPolicyPageV2
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode data-security policy page: %w", err)
		}
		if page.Data == nil {
			return nil, fmt.Errorf("data-security policy page is missing data")
		}
		for _, item := range *page.Data {
			policy, err := item.AsModelsDataSecurityPolicy()
			if err != nil {
				return nil, fmt.Errorf("decode data-security policy page item: %w", err)
			}
			if err := validatePolicy(policy, ""); err != nil {
				return nil, err
			}
			if seenIDs[policy.Id] {
				return nil, fmt.Errorf("data-security policy pagination returned duplicate ID %q", policy.Id)
			}
			seenIDs[policy.Id] = true
			result = append(result, policy)
		}
		cursor, err := nextCursor(page, organizationID)
		if err != nil {
			return nil, err
		}
		if cursor == "" {
			return result, nil
		}
		if seenCursors[cursor] {
			return nil, fmt.Errorf("data-security policy pagination repeated a cursor")
		}
		seenCursors[cursor] = true
		params.Cursor = &cursor
	}
}

func nextCursor(page generated.ModelsPolicyPageV2, organizationID string) (string, error) {
	if page.Meta != nil && page.Meta.Next != nil && *page.Meta.Next != "" {
		return *page.Meta.Next, nil
	}
	if page.Links == nil || page.Links.Next == nil || *page.Links.Next == "" {
		return "", nil
	}
	link, err := url.Parse(*page.Links.Next)
	if err != nil {
		return "", fmt.Errorf("parse next data-security policy page: %w", err)
	}
	expected := "/admin/control/v2/orgs/" + url.PathEscape(organizationID) + "/policies"
	if link.User != nil || link.Fragment != "" || link.EscapedPath() != expected ||
		(link.IsAbs() && (link.Scheme != "https" || link.Host != "api.atlassian.com")) ||
		(!link.IsAbs() && link.Host != "") {
		return "", fmt.Errorf("next data-security policy page does not refer to the expected Control API endpoint")
	}
	values, err := url.ParseQuery(link.RawQuery)
	if err != nil {
		return "", fmt.Errorf("parse next data-security policy query: %w", err)
	}
	cursors := values["cursor"]
	if len(cursors) != 1 || cursors[0] == "" {
		return "", fmt.Errorf("next data-security policy page is missing a unique cursor")
	}
	return cursors[0], nil
}

func (s *Service) CreateDataSecurityPolicy(ctx context.Context, organizationID string, request generated.CreateDataSecurityPolicyJSONRequestBody) (generated.ModelsDataSecurityPolicy, error) { //nolint:staticcheck // Atlassian exposes no non-deprecated public Control API replacement.
	response, err := s.client.CreateDataSecurityPolicy(admin.WithoutRetry(ctx), organizationID, request) //nolint:staticcheck // No public replacement exists.
	body, err := responseBody(response, err, http.StatusAccepted)
	if err != nil {
		return generated.ModelsDataSecurityPolicy{}, err
	}
	return decodePolicy(body, "")
}

func (s *Service) UpdateDataSecurityPolicy(ctx context.Context, organizationID, policyID string, request generated.UpdateDataSecurityPolicyJSONRequestBody) error { //nolint:staticcheck // Atlassian exposes no non-deprecated public Control API replacement.
	response, err := s.client.UpdateDataSecurityPolicy(admin.WithoutRetry(ctx), organizationID, policyID, request) //nolint:staticcheck // No public replacement exists.
	body, err := responseBody(response, err, http.StatusAccepted)
	if err != nil {
		return err
	}
	_, err = decodePolicy(body, policyID)
	return err
}

func (s *Service) DeleteDataSecurityPolicy(ctx context.Context, organizationID, policyID string) error {
	response, err := s.client.DeleteDataSecurityPolicy(admin.WithoutRetry(ctx), organizationID, policyID)
	_, err = responseBody(response, err, http.StatusAccepted)
	return err
}
