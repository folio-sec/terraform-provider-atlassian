package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
)

func policyBody(response *http.Response, requestErr error, expected int) ([]byte, error) {
	if requestErr != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, fmt.Errorf("policy request: %w", requestErr)
	}
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("policy request returned no response")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read policy response: %w", err)
	}
	// Check status before decoding: bare or non-JSON 404 responses still mean absent.
	if response.StatusCode != expected {
		return nil, responseError(response, body)
	}
	return body, nil
}

func decodePolicy(body []byte, expectedID string) (generated.PolicyModel, error) {
	var result generated.Policy
	if err := json.Unmarshal(body, &result); err != nil {
		return generated.PolicyModel{}, fmt.Errorf("decode policy: %w", err)
	}
	if result.Data == nil {
		return generated.PolicyModel{}, fmt.Errorf("policy response is missing data")
	}
	if err := validatePolicyResponse(*result.Data); err != nil {
		return *result.Data, err
	}
	if expectedID != "" && result.Data.Id != expectedID {
		return generated.PolicyModel{}, fmt.Errorf("policy response ID does not match requested ID")
	}
	return *result.Data, nil
}

func validatePolicyResponse(policy generated.PolicyModel) error {
	if strings.TrimSpace(policy.Id) == "" || policy.Type != "policy" || strings.TrimSpace(policy.Attributes.Type) == "" {
		return fmt.Errorf("policy response is missing identity or type")
	}
	return nil
}

func (s *Service) GetPolicyById(ctx context.Context, organizationID, policyID string) (generated.PolicyModel, error) {
	response, err := s.policies.GetPolicyById(ctx, organizationID, policyID)
	body, err := policyBody(response, err, http.StatusOK)
	if err != nil {
		return generated.PolicyModel{}, err
	}
	return decodePolicy(body, policyID)
}

func (s *Service) GetPolicies(ctx context.Context, organizationID string, policyType *string) ([]generated.PolicyModel, error) {
	params := &generated.GetPoliciesParams{Type: policyType}
	policies := make([]generated.PolicyModel, 0)
	seenCursors := map[string]bool{}
	seenIDs := map[string]bool{}
	for {
		response, err := s.policies.GetPolicies(ctx, organizationID, params)
		body, err := policyBody(response, err, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var page generated.PolicyPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode policy page: %w", err)
		}
		if page.Data == nil {
			return nil, fmt.Errorf("policy page is missing data")
		}
		for _, policy := range *page.Data {
			if err := validatePolicyResponse(policy); err != nil {
				return nil, err
			}
			// Concurrent pages must not produce two different set elements for one ID.
			if seenIDs[policy.Id] {
				return nil, fmt.Errorf("policy pagination returned duplicate ID %q; retry the read", policy.Id)
			}
			seenIDs[policy.Id] = true
			policies = append(policies, policy)
		}
		cursor, err := policyNextCursor(page, organizationID)
		if err != nil {
			return nil, err
		}
		if cursor == "" {
			return policies, nil
		}
		if seenCursors[cursor] {
			return nil, fmt.Errorf("policy pagination repeated a cursor")
		}
		seenCursors[cursor] = true
		params.Cursor = &cursor
	}
}

func policyNextCursor(page generated.PolicyPage, organizationID string) (string, error) {
	if page.Meta != nil && page.Meta.Next.IsSpecified() && !page.Meta.Next.IsNull() {
		value, err := page.Meta.Next.Get()
		if err != nil {
			return "", fmt.Errorf("read policy cursor: %w", err)
		}
		if value != "" {
			return value, nil
		}
	}
	if page.Links == nil || page.Links.Next == nil || *page.Links.Next == "" {
		return "", nil
	}
	return policyLinkCursor(*page.Links.Next, organizationID)
}

func policyLinkCursor(next, organizationID string) (string, error) {
	link, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("parse next policy page: %w", err)
	}
	expected := "/admin/v1/orgs/" + url.PathEscape(organizationID) + "/policies"
	if link.User != nil || link.Fragment != "" || (link.IsAbs() && (link.Scheme != "https" || link.Host != "api.atlassian.com")) || (!link.IsAbs() && link.Host != "") || (link.EscapedPath() != expected && link.EscapedPath() != strings.TrimPrefix(expected, "/admin")) {
		return "", fmt.Errorf("next policy page does not refer to the expected Organization API endpoint")
	}
	values, err := url.ParseQuery(link.RawQuery)
	if err != nil {
		return "", fmt.Errorf("parse next policy query: %w", err)
	}
	cursors := values["cursor"]
	if len(cursors) != 1 || cursors[0] == "" {
		return "", fmt.Errorf("next policy page is missing a unique cursor")
	}
	return cursors[0], nil
}

func (s *Service) CreatePolicy(ctx context.Context, organizationID string, request generated.CreatePolicyJSONRequestBody) (generated.PolicyModel, error) {
	response, err := s.policies.CreatePolicy(admin.WithoutRetry(ctx), organizationID, request)
	body, err := policyBody(response, err, http.StatusAccepted)
	if err != nil {
		return generated.PolicyModel{}, err
	}
	return decodePolicy(body, "")
}

func (s *Service) UpdatePolicy(ctx context.Context, organizationID, policyID string, request generated.UpdatePolicyJSONRequestBody) error {
	response, err := s.policies.UpdatePolicy(admin.WithoutRetry(ctx), organizationID, policyID, request)
	body, err := policyBody(response, err, http.StatusAccepted)
	if err != nil {
		return err
	}
	_, err = decodePolicy(body, policyID)
	return err
}

func (s *Service) DeletePolicy(ctx context.Context, organizationID, policyID string) error {
	response, err := s.policies.DeletePolicy(admin.WithoutRetry(ctx), organizationID, policyID)
	_, err = policyBody(response, err, http.StatusAccepted)
	return err
}
