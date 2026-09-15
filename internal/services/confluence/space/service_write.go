package space

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	v1gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v1/generated"
	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
	"github.com/oapi-codegen/nullable"
)

// deletePollInterval and deletePollTimeout govern the delete long-task poll.
// Gate 7 (plans/confluence-space-verification.json) showed both gate
// deletions finished inside the first 5-second poll, so 2s/5m is generous
// rather than tight.
// errorBodyLimit caps how much of a non-2xx body is read for diagnostics.
const errorBodyLimit = 1 << 20

const (
	deletePollInterval = 2 * time.Second
	deletePollTimeout  = 5 * time.Minute
)

// ErrRequestNotSent marks a failure that happened before the request left the
// client: a bad input converted locally, or a transport that could not even be
// built. The outcome of such a failure is not ambiguous — nothing reached
// Atlassian — so callers must not treat it as "the server may or may not have
// applied this" and go looking for a space to adopt.
var ErrRequestNotSent = errors.New("request was not sent")

// RoleAssignment is a create-time-only principal/role pairing accepted by
// createSpace. There is no ongoing lifecycle for it in this resource; see
// plans/confluence-space.md, "Create-only attributes".
type RoleAssignment struct {
	PrincipalType string
	PrincipalID   string
	RoleID        string
}

// CreateSpaceRequest models the createSpace request body.
type CreateSpaceRequest struct {
	Key                          string
	Alias                        string
	Name                         string
	Description                  *Description
	TemplateKey                  string
	CopySpaceAccessConfiguration string
	CreatePrivateSpace           *bool
	RoleAssignments              []RoleAssignment
}

// CreateSpaceResult is the identity createSpace returns. It is deliberately
// minimal: spaceOwnerId is absent from the create response even though it is
// present on read (verified), so callers must re-read via GetSpaceByID before
// writing any state rather than trusting this result for anything but
// resolving the space to read.
type CreateSpaceResult struct {
	ID        string
	Key       string
	CreatedAt *time.Time
}

// CreateSpace creates a space via v2 createSpace. The operation is
// non-idempotent, so this method marks its own request WithoutRetry; the
// transport then sends it once. A failure that leaves the outcome unresolved
// is reported by the caller rather than retried or reconciled by a lookup.
func (s *Service) CreateSpace(ctx context.Context, req CreateSpaceRequest) (CreateSpaceResult, error) {
	const operation = "create space"
	v2, err := s.client.V2(ctx)
	if err != nil {
		return CreateSpaceResult{}, fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
	}

	body := v2gen.CreateSpaceJSONRequestBody{Name: req.Name}
	if req.Key != "" {
		key := req.Key
		body.Key = &key
	}
	if req.Alias != "" {
		alias := req.Alias
		body.Alias = &alias
	}
	if req.Description != nil {
		representation := req.Description.Representation
		value := req.Description.Value
		body.Description = &struct {
			Representation *string `json:"representation,omitempty"`
			Value          *string `json:"value,omitempty"`
		}{Representation: &representation, Value: &value}
	}
	if req.TemplateKey != "" {
		templateKey := req.TemplateKey
		body.TemplateKey = &templateKey
	}
	if req.CopySpaceAccessConfiguration != "" {
		id, err := strconv.Atoi(strings.TrimSpace(req.CopySpaceAccessConfiguration))
		if err != nil {
			return CreateSpaceResult{}, fmt.Errorf("%s: %w: parse copy_space_access_configuration %q: %w", operation, ErrRequestNotSent, req.CopySpaceAccessConfiguration, err)
		}
		body.CopySpaceAccessConfiguration = &id
	}
	if req.CreatePrivateSpace != nil {
		body.CreatePrivateSpace = req.CreatePrivateSpace
	}
	if len(req.RoleAssignments) > 0 {
		assignments := make([]struct {
			Principal *v2gen.Principal `json:"principal,omitempty"`
			RoleId    *string          `json:"roleId,omitempty"`
		}, len(req.RoleAssignments))
		for i, assignment := range req.RoleAssignments {
			principalType := v2gen.PrincipalType(assignment.PrincipalType)
			principalID := assignment.PrincipalID
			roleID := assignment.RoleID
			assignments[i].Principal = &v2gen.Principal{PrincipalId: &principalID, PrincipalType: &principalType}
			assignments[i].RoleId = &roleID
		}
		body.RoleAssignments = &assignments
	}

	resp, err := v2.CreateSpaceWithResponse(confluence.WithoutRetry(ctx), body)
	if err != nil {
		// The generated client runs the request editors before it sends, so a
		// failure to authenticate -- a token exchange that could not complete,
		// say -- arrives here having sent nothing. Classify it that way, or
		// the caller treats an authentication problem as an unresolved create.
		if resp == nil && errors.Is(err, confluence.ErrAuthorize) {
			return CreateSpaceResult{}, fmt.Errorf("%s: %w: %w", operation, ErrRequestNotSent, err)
		}
		return CreateSpaceResult{}, fmt.Errorf("%s: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodPost, "/spaces", resp.StatusCode(), resp.Body); err != nil {
		return CreateSpaceResult{}, fmt.Errorf("%s: %w", operation, err)
	}
	if resp.JSON201 == nil {
		return CreateSpaceResult{}, fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	id, err := requireField(operation, "id", resp.JSON201.Id)
	if err != nil {
		return CreateSpaceResult{}, err
	}
	// The key is not required to proceed: the caller reads the space by id and
	// takes the key from that read. Demanding it here would discard an id the
	// server already gave us, leaving a created space untrackable over a field
	// nothing needs yet.
	key := ""
	if resp.JSON201.Key != nil {
		key = strings.TrimSpace(*resp.JSON201.Key)
	}
	return CreateSpaceResult{ID: id, Key: key, CreatedAt: resp.JSON201.CreatedAt}, nil
}

// UpdateSpaceRequest carries only the fields to change. v1 updateSpace is a
// genuine partial update (verified: a body containing only name preserved the
// description), so a nil field here is simply omitted from the request
// rather than resent with its previous value.
type UpdateSpaceRequest struct {
	Name        *string
	Description *Description
	HomepageID  *string
	Type        *string
	Status      *string
}

// UpdateSpace sends only the changed fields to v1 updateSpace, keyed by
// spaceKey. The response body is intentionally not parsed beyond the status
// code: it was measured at roughly 80KB, dominated by permissions, and the
// caller is expected to re-read the space through v2 (the same read the
// resource's Read performs) rather than rely on it.
func (s *Service) UpdateSpace(ctx context.Context, spaceKey string, req UpdateSpaceRequest) error {
	const operation = "update space"
	spaceKey = strings.TrimSpace(spaceKey)
	if spaceKey == "" {
		return fmt.Errorf("%s: space key is empty in state; refusing to send a request to /wiki/rest/api/space/", operation)
	}
	v1, err := s.client.V1(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}

	var body v1gen.SpaceUpdate
	if req.Name != nil {
		body.Name = nullable.NewNullableWithValue(*req.Name)
	}
	if req.Description != nil {
		body.Description = nullable.NewNullableWithValue(v1gen.SpaceDescriptionCreate{
			Plain: v1gen.SpaceDescriptionCreate_Plain{Value: &req.Description.Value, Representation: &req.Description.Representation},
		})
	}
	if req.HomepageID != nil {
		body.Homepage = nullable.NewNullableWithValue(map[string]interface{}{"id": *req.HomepageID})
	}
	if req.Type != nil {
		body.Type = req.Type
	}
	if req.Status != nil {
		body.Status = nullable.NewNullableWithValue(*req.Status)
	}

	// Take the raw response rather than the ...WithResponse helper: that helper
	// unmarshals a 200 body into the generated Space model, and this body was
	// measured at roughly 80 KB dominated by permissions. Decoding it would
	// turn a committed update into a reported failure whenever the payload
	// does not fit the generated model. Only the status matters here; the
	// caller re-reads through v2.
	raw, err := v1.UpdateSpace(ctx, spaceKey, body)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	defer func() { _ = raw.Body.Close() }()
	errorBody, err := io.ReadAll(io.LimitReader(raw.Body, errorBodyLimit))
	if err != nil {
		return fmt.Errorf("%s: read response: %w", operation, err)
	}
	if err := confluence.CheckResponse(http.MethodPut, "/wiki/rest/api/space/"+spaceKey, raw.StatusCode, errorBody); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

// DeleteSpace deletes a space via v1 deleteSpace, keyed by spaceKey, and
// blocks until the resulting long-running task finishes. A 404 from the
// delete call itself is treated as already deleted.
func (s *Service) DeleteSpace(ctx context.Context, spaceKey string) error {
	const operation = "delete space"
	spaceKey = strings.TrimSpace(spaceKey)
	if spaceKey == "" {
		return fmt.Errorf("%s: space key is empty in state; refusing to send a request to /wiki/rest/api/space/", operation)
	}
	v1, err := s.client.V1(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}

	resp, err := v1.DeleteSpaceWithResponse(confluence.WithoutRetry(ctx), spaceKey)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil
	}
	if err := confluence.CheckResponse(http.MethodDelete, "/wiki/rest/api/space/"+spaceKey, resp.StatusCode(), resp.Body); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if resp.JSON202 == nil {
		return fmt.Errorf("%s: API returned an invalid success response", operation)
	}
	if err := s.waitForTask(ctx, resp.JSON202.Id); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

// waitForTask polls v1 getTask by id, against the configured base URL, until
// the task reports finished or the bounded timeout elapses.
//
// It never requests LongTask.Links.Status: that URL was observed to be
// "/rest/api/longtask/{id}", missing the "/wiki" prefix, and 404s in both
// authentication modes (plans/confluence-space.md, "Never trust a
// server-supplied URL"). Polling by id against the client's own base is the
// one thing the generated client already does correctly.
func (s *Service) waitForTask(ctx context.Context, taskID string) error {
	v1, err := s.client.V1(ctx)
	if err != nil {
		return fmt.Errorf("poll delete task: %w", err)
	}

	pollCtx, cancel := context.WithTimeout(ctx, deletePollTimeout)
	defer cancel()
	ticker := time.NewTicker(deletePollInterval)
	defer ticker.Stop()

	for {
		resp, err := v1.GetTaskWithResponse(pollCtx, taskID)
		if err != nil {
			return fmt.Errorf("poll delete task %s: %w", taskID, err)
		}
		if err := confluence.CheckResponse(http.MethodGet, "/wiki/rest/api/longtask/"+taskID, resp.StatusCode(), resp.Body); err != nil {
			return fmt.Errorf("poll delete task %s: %w", taskID, err)
		}
		if resp.JSON200 == nil {
			return fmt.Errorf("poll delete task %s: API returned an invalid success response", taskID)
		}
		if resp.JSON200.Finished {
			if !resp.JSON200.Successful {
				return fmt.Errorf("delete task %s did not succeed: %s", taskID, taskMessages(resp.JSON200.Messages))
			}
			return nil
		}

		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out after %s waiting for delete task %s to finish", deletePollTimeout, taskID)
		case <-ticker.C:
		}
	}
}

func taskMessages(messages []v1gen.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Translation != nil && strings.TrimSpace(*message.Translation) != "" {
			parts = append(parts, *message.Translation)
		}
	}
	if len(parts) == 0 {
		return "no message reported"
	}
	return strings.Join(parts, "; ")
}
