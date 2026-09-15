// Package space implements the read path for Confluence Cloud spaces: a
// handwritten Service over the generated v2 client, and the two Terraform
// data sources built on it. See plans/confluence-space.md for the design this
// package follows.
package space

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
)

// pageLimit is the page size requested from GetSpaces. Pagination itself is
// internal to the provider (CLAUDE.md: follow API pagination internally
// unless pagination is part of the Terraform use case), so a comfortably
// large page keeps the number of requests low without the caller ever seeing
// a page boundary.
const pageLimit = 250

// descriptionFormatPlain is always sent, on both operations. Without it the
// API returns description as {} even when a description exists (gate 2,
// plans/confluence-space-verification.json), and it is the one exception to
// never sending a response-shaping parameter: it costs no OAuth scope.
var descriptionFormatPlain = v2gen.SpaceDescriptionBodyRepresentation("plain")

// Description is the value/representation pair the API returns under
// description.plain once description-format=plain is requested.
type Description struct {
	Value          string
	Representation string
}

// Space is the Terraform-facing model for a Confluence space, assembled from
// whichever v2 read operation produced it.
type Space struct {
	ID           string
	Key          string
	Name         string
	Type         string
	Status       string
	AuthorID     string
	SpaceOwnerID *string
	HomepageID   *string
	CreatedAt    *time.Time
	// CurrentActiveAlias is documented on SpaceBulk only in the upstream
	// specification, but getSpaceById was observed to return it as well; the
	// Overlay adds it to SpaceSingle too (api/confluence/v2/overlay.yaml), so
	// it is populated here regardless of which operation produced the Space.
	CurrentActiveAlias *string
	Description        *Description
}

// GetSpacesFilters holds the getSpaces query operands that change which
// spaces match. Sort, cursor, limit, description-format and include-icon are
// response-shaping or pagination controls and stay internal to this package,
// mirroring atlassian_organization_users.
type GetSpacesFilters struct {
	IDs            []string
	Keys           []string
	Type           string
	Status         string
	Labels         []string
	FavoritedBy    string
	NotFavoritedBy string
}

// Service implements Confluence space read operations on top of the
// generated v2 client. It holds the transport, not the generated client:
// confluence.Client.V2 is lazy and resolves the base URL on first use, so it
// is called once per operation rather than cached here.
type Service struct {
	client *confluence.Client
}

// NewService wraps a configured Confluence transport.
func NewService(client *confluence.Client) *Service {
	return &Service{client: client}
}

// GetSpaceByID reads one space by its numeric-string ID. description-format
// is always plain; no include-* parameter is ever sent, because each one
// converts into another OAuth scope a service account credential must be
// granted, and the 50-scope cap makes minimizing that spend a design
// constraint (plans/confluence-space.md, "Scope budget (mode B)").
func (s *Service) GetSpaceByID(ctx context.Context, id string) (Space, error) {
	const operation = "get space by id"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return Space{}, fmt.Errorf("%s: %w", operation, err)
	}

	format := descriptionFormatPlain
	resp, err := v2.GetSpaceByIdWithResponse(ctx, id, &v2gen.GetSpaceByIdParams{DescriptionFormat: &format})
	if err != nil {
		return Space{}, fmt.Errorf("%s: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodGet, "/spaces/"+id, resp.StatusCode(), resp.Body); err != nil {
		return Space{}, fmt.Errorf("%s: %w", operation, err)
	}
	if resp.JSON200 == nil {
		return Space{}, fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	body := resp.JSON200

	spaceID, err := requireField(operation, "id", body.Id)
	if err != nil {
		return Space{}, err
	}
	key, err := requireField(operation, "key", body.Key)
	if err != nil {
		return Space{}, err
	}
	name, err := requireField(operation, "name", body.Name)
	if err != nil {
		return Space{}, err
	}
	spaceType, err := requireField(operation, "type", body.Type)
	if err != nil {
		return Space{}, err
	}
	status, err := requireField(operation, "status", body.Status)
	if err != nil {
		return Space{}, err
	}
	authorID, err := requireField(operation, "authorId", body.AuthorId)
	if err != nil {
		return Space{}, err
	}

	return Space{
		ID:                 spaceID,
		Key:                key,
		Name:               name,
		Type:               spaceType,
		Status:             status,
		AuthorID:           authorID,
		SpaceOwnerID:       body.SpaceOwnerId,
		HomepageID:         body.HomepageId,
		CreatedAt:          body.CreatedAt,
		CurrentActiveAlias: body.CurrentActiveAlias,
		Description:        descriptionFromGenerated(body.Description),
	}, nil
}

// GetSpaces follows every page of getSpaces internally and returns every
// space matching filters. description-format is always plain and no
// include-* parameter is ever sent, for the same reasons as GetSpaceByID.
func (s *Service) GetSpaces(ctx context.Context, filters GetSpacesFilters) ([]Space, error) {
	const operation = "get spaces"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	params := spacesParams(filters)

	var spaces []Space
	seenCursors := map[string]struct{}{}
	for {
		resp, err := v2.GetSpacesWithResponse(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		if err := confluence.CheckResponse(http.MethodGet, "/spaces", resp.StatusCode(), resp.Body); err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("%s: API returned an invalid success response", operation)
		}
		if resp.JSON200.Results != nil {
			for _, bulk := range *resp.JSON200.Results {
				converted, err := spaceFromBulk(bulk)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", operation, err)
				}
				spaces = append(spaces, converted)
			}
		}

		next := ""
		if resp.JSON200.UnderscoreLinks != nil && resp.JSON200.UnderscoreLinks.Next != nil {
			next = *resp.JSON200.UnderscoreLinks.Next
		}
		if next == "" {
			return spaces, nil
		}
		cursor, err := cursorFromNextLink(next)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		if _, exists := seenCursors[cursor]; exists {
			return nil, fmt.Errorf("%s: API returned repeated pagination cursor %q", operation, cursor)
		}
		seenCursors[cursor] = struct{}{}
		params.Cursor = &cursor
	}
}

// cursorFromNextLink extracts the cursor query parameter from _links.next
// instead of joining or following it as a URL. _links.next is site-relative
// and _links.base names the site even under the gateway that mode B uses, so
// joining them would either send a gateway bearer token to the site or drop
// the cloud id from the request path. See plans/confluence-space.md, "Never
// trust a server-supplied URL".
func cursorFromNextLink(next string) (string, error) {
	parsed, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("parse pagination link %q: %w", next, err)
	}
	cursor := parsed.Query().Get("cursor")
	if cursor == "" {
		// The API offered another page but not the cursor to reach it.
		// Returning no cursor here would silently truncate the result, so
		// report the broken contract instead.
		return "", fmt.Errorf("pagination link %q carries no cursor", next)
	}
	return cursor, nil
}

// spacesParams builds the getSpaces query, always requesting the plain
// description and never an include-* parameter. Ids is []string in the
// generated client: the Overlay retypes it to match the id path parameter
// (api/confluence/v2/overlay.yaml), so space ids stay opaque strings end to
// end and no conversion is needed here.
func spacesParams(filters GetSpacesFilters) *v2gen.GetSpacesParams {
	format := descriptionFormatPlain
	limit := int32(pageLimit)
	params := &v2gen.GetSpacesParams{
		DescriptionFormat: &format,
		Limit:             &limit,
	}

	if len(filters.IDs) > 0 {
		ids := append([]string(nil), filters.IDs...)
		params.Ids = &ids
	}
	if len(filters.Keys) > 0 {
		keys := append([]string(nil), filters.Keys...)
		params.Keys = &keys
	}
	if filters.Type != "" {
		spaceType := v2gen.GetSpacesParamsType(filters.Type)
		params.Type = &spaceType
	}
	if filters.Status != "" {
		status := v2gen.GetSpacesParamsStatus(filters.Status)
		params.Status = &status
	}
	if len(filters.Labels) > 0 {
		labels := append([]string(nil), filters.Labels...)
		params.Labels = &labels
	}
	if filters.FavoritedBy != "" {
		favoritedBy := filters.FavoritedBy
		params.FavoritedBy = &favoritedBy
	}
	if filters.NotFavoritedBy != "" {
		notFavoritedBy := filters.NotFavoritedBy
		params.NotFavoritedBy = &notFavoritedBy
	}
	return params
}

func spaceFromBulk(bulk v2gen.SpaceBulk) (Space, error) {
	const operation = "get spaces"
	id, err := requireField(operation, "id", bulk.Id)
	if err != nil {
		return Space{}, err
	}
	key, err := requireField(operation, "key", bulk.Key)
	if err != nil {
		return Space{}, err
	}
	name, err := requireField(operation, "name", bulk.Name)
	if err != nil {
		return Space{}, err
	}
	spaceType, err := requireField(operation, "type", bulk.Type)
	if err != nil {
		return Space{}, err
	}
	status, err := requireField(operation, "status", bulk.Status)
	if err != nil {
		return Space{}, err
	}
	authorID, err := requireField(operation, "authorId", bulk.AuthorId)
	if err != nil {
		return Space{}, err
	}
	return Space{
		ID:                 id,
		Key:                key,
		Name:               name,
		Type:               spaceType,
		Status:             status,
		AuthorID:           authorID,
		SpaceOwnerID:       bulk.SpaceOwnerId,
		HomepageID:         bulk.HomepageId,
		CreatedAt:          bulk.CreatedAt,
		CurrentActiveAlias: bulk.CurrentActiveAlias,
		Description:        descriptionFromGenerated(bulk.Description),
	}, nil
}

func descriptionFromGenerated(description *v2gen.SpaceDescription) *Description {
	if description == nil || description.Plain == nil {
		return nil
	}
	value := ""
	if description.Plain.Value != nil {
		value = *description.Plain.Value
	}
	representation := ""
	if description.Plain.Representation != nil {
		representation = *description.Plain.Representation
	}
	return &Description{Value: value, Representation: representation}
}

// requireField reports an error naming the missing field when value is nil or
// blank. T is constrained to string-based types so it covers both the plain
// *string fields (id, key, name, authorId) and the enum-typed ones (type,
// status).
func requireField[T ~string](operation, field string, value *T) (string, error) {
	if value == nil || strings.TrimSpace(string(*value)) == "" {
		return "", fmt.Errorf("%s: API returned space without %s", operation, field)
	}
	return string(*value), nil
}

// GetSpaceByKey resolves the one space whose key matches exactly. v2 has no
// read-by-key operation, so this filters getSpaces, and it re-checks the key
// on the results rather than trusting the filter: whether keys= matches
// exactly or by prefix is not something these gates established, and a
// caller that took the first result would silently bind the wrong space. It
// returns a not-found error for no match, mirroring getSpaceById's 404.
func (s *Service) GetSpaceByKey(ctx context.Context, key string) (Space, error) {
	const operation = "get space by key"
	spaces, err := s.GetSpaces(ctx, GetSpacesFilters{Keys: []string{key}})
	if err != nil {
		return Space{}, err
	}
	matches := make([]Space, 0, 1)
	for _, space := range spaces {
		if space.Key == key {
			matches = append(matches, space)
		}
	}
	switch len(matches) {
	case 0:
		return Space{}, fmt.Errorf("%s: not found: no Confluence space with key %q", operation, key)
	case 1:
		return matches[0], nil
	default:
		// Keys are documented as unique site-wide. If that ever stops holding,
		// say so instead of choosing one arbitrarily.
		return Space{}, fmt.Errorf("%s: %d Confluence spaces returned for key %q", operation, len(matches), key)
	}
}
