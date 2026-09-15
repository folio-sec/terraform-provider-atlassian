package confluence

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// HTTPError is a non-2xx response from Confluence or from the gateway in
// front of it. Message is the most specific explanation the response body
// offered; Body is the trimmed raw body for the cases where it did not.
type HTTPError struct {
	StatusCode int
	Method     string
	URL        string
	Message    string
	Body       string
	// GatewayRouting marks a response the api.atlassian.com gateway produced
	// because it could not route the request, rather than one Confluence
	// produced about a space. The distinction matters most for 404: a routing
	// 404 means the request never reached Confluence, so it says nothing
	// about whether the space exists.
	GatewayRouting bool
}

func (e *HTTPError) Error() string {
	text := fmt.Sprintf("Confluence API returned %s for %s %s", http.StatusText(e.StatusCode), e.Method, e.URL)
	switch {
	case e.Message != "":
		return text + ": " + e.Message
	case e.Body != "":
		return text + ": " + e.Body
	default:
		return text
	}
}

// IsNotFound reports whether the API answered that the resource does not
// exist. A 404 the gateway produced because it could not route the request is
// deliberately excluded: treating it as absence would let a misconfigured
// cloud id drop a live space out of state, and would let a destroy record a
// space as deleted while it is still there.
func IsNotFound(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound && !httpErr.GatewayRouting
}

// CheckResponse converts a non-2xx response into an HTTPError, or returns nil
// for a successful status. Three error body shapes are recognized, because a
// service account request crosses the api.atlassian.com gateway before it
// reaches Confluence and either layer may answer:
//
//   - Confluence v2: {"errors":[{"status","code","title","detail"}]}
//   - the gateway:   {"timestamp","status","error","path","message"}
//   - Admin-style:   {"message","details"}
//
// Anything else is reported as its trimmed body.
func CheckResponse(method, requestURL string, statusCode int, body []byte) error {
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return nil
	}
	httpErr := &HTTPError{
		StatusCode: statusCode,
		Method:     method,
		URL:        requestURL,
		Body:       truncate(strings.TrimSpace(string(body)), 1024),
	}
	message, fromGateway := parseErrorBody(body)
	httpErr.Message = message
	httpErr.GatewayRouting = fromGateway

	switch {
	case statusCode == http.StatusForbidden:
		httpErr.Message = appendHint(httpErr.Message, "The credential is not permitted to perform this operation. For a service account, check that its OAuth scopes include the ones this operation requires; for basic auth, check the account's space permissions.")
	case statusCode == http.StatusNotFound && fromGateway:
		httpErr.Message = appendHint(httpErr.Message, "The api.atlassian.com gateway did not route the request. This usually means service_account.cloud_id does not identify the site; compare it with https://<site>/_edge/tenant_info.")
	}
	return httpErr
}

// parseErrorBody extracts a human-readable message from the known error
// shapes. fromGateway reports the gateway shape specifically, because a
// gateway 404 has a different likely cause than a Confluence 404.
func parseErrorBody(body []byte) (message string, fromGateway bool) {
	var confluenceShape struct {
		Errors []struct {
			Status int    `json:"status"`
			Code   string `json:"code"`
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &confluenceShape); err == nil && len(confluenceShape.Errors) > 0 {
		parts := make([]string, 0, len(confluenceShape.Errors))
		for _, e := range confluenceShape.Errors {
			part := strings.TrimSpace(strings.TrimSpace(e.Code + " " + e.Title))
			if e.Detail != "" {
				part += " (" + e.Detail + ")"
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, "; "), false
	}

	var flatShape struct {
		Message string          `json:"message"`
		Error   string          `json:"error"`
		Path    string          `json:"path"`
		Details json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(body, &flatShape); err == nil && (flatShape.Message != "" || flatShape.Error != "") {
		isGateway := flatShape.Path != ""
		message := flatShape.Message
		if flatShape.Error != "" && flatShape.Error != message {
			message = strings.TrimSpace(strings.Trim(flatShape.Error+": "+message, ": "))
		}
		if len(flatShape.Details) > 0 && string(flatShape.Details) != "null" {
			message += " " + truncate(string(flatShape.Details), 256)
		}
		return strings.TrimSpace(message), isGateway
	}
	return "", false
}

func appendHint(message, hint string) string {
	if message == "" {
		return hint
	}
	return message + ". " + hint
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
