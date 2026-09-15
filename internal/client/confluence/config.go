package confluence

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	v1gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v1/generated"
	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// AuthMode selects how the Confluence transport authenticates and, because
// the two modes are served from different hosts, which base URL it uses.
type AuthMode int

const (
	// AuthBasic authenticates as an Atlassian account with its email and API
	// token against the site domain.
	AuthBasic AuthMode = iota + 1
	// AuthServiceAccount authenticates as a service account with OAuth 2.0
	// client credentials against the api.atlassian.com gateway.
	AuthServiceAccount
)

func (m AuthMode) String() string {
	switch m {
	case AuthBasic:
		return "basic_auth"
	case AuthServiceAccount:
		return "service_account"
	default:
		return fmt.Sprintf("AuthMode(%d)", int(m))
	}
}

// Config is the fully resolved Confluence configuration. The provider merges
// configuration and environment before building it, so every field here is a
// final value; presence and combination rules that need to name the source of
// a value are enforced by the provider, and New only defends against a
// configuration that is internally inconsistent.
type Config struct {
	// SiteURL is the Confluence Cloud site, for example
	// https://example.atlassian.net. Required for AuthBasic. For
	// AuthServiceAccount it is used only to discover CloudID when that is
	// empty.
	SiteURL string

	Mode AuthMode

	// AuthBasic credentials.
	Email    string
	APIToken string

	// AuthServiceAccount credentials.
	ClientID     string
	ClientSecret string
	// CloudID is the site's cloud id. Optional for AuthServiceAccount: when
	// empty it is discovered from SiteURL on first use.
	CloudID string

	// HTTPClient is the underlying client for every request, including the
	// token exchange and cloud id discovery. Nil uses http.DefaultClient.
	HTTPClient *http.Client
}

// Client is the Confluence transport shared by the v1 and v2 generated
// clients. It owns the resolved credentials, the base URL rules, retry policy,
// and the lazily resolved site identity.
type Client struct {
	mode    AuthMode
	site    *url.URL
	cloudID string

	email, apiToken        string
	clientID, clientSecret string

	options options
	// httpClient serves the API calls and honours WithoutRetry. helperClient
	// serves the token exchange and cloud id discovery, which are idempotent,
	// are not the mutation a caller marked WithoutRetry, and must not run
	// through a policy that reaches back into the authenticator.
	httpClient   *retryablehttp.Client
	helperClient *retryablehttp.Client
	auth         authenticator

	// mu guards the lazily built state below. It is a mutex rather than a
	// sync.Once so that a failed discovery is retried on the next use instead
	// of being cached for the life of the provider.
	mu sync.Mutex
	v1 *v1gen.ClientWithResponses
	v2 *v2gen.ClientWithResponses
}

const gatewayHost = "https://api.atlassian.com"

// options holds the endpoints and timings that tests substitute.
type options struct {
	gateway      string
	tokenURL     string
	retryWaitMin time.Duration
	retryWaitMax time.Duration
}

func defaultOptions() options {
	return options{
		gateway:      gatewayHost,
		tokenURL:     defaultOAuthEndpoint,
		retryWaitMin: time.Second,
		retryWaitMax: 30 * time.Second,
	}
}

// New validates a resolved configuration and returns a Client. It performs no
// network I/O: cloud id discovery and the token exchange happen on first use
// so that configuring the provider never depends on Confluence reachability.
func New(config Config) (*Client, error) {
	return newClient(config, defaultOptions())
}

// NewForTest builds a client whose gateway and OAuth token endpoint are the
// given base URL. It exists so tests in sibling packages can drive the service
// account path against an httptest server; production code calls New.
func NewForTest(config Config, baseURL string) (*Client, error) {
	opts := defaultOptions()
	opts.gateway = baseURL
	opts.tokenURL = baseURL + "/oauth/token"
	opts.retryWaitMin, opts.retryWaitMax = time.Millisecond, 5*time.Millisecond
	return newClient(config, opts)
}

func newClient(config Config, opts options) (*Client, error) {
	c := &Client{
		mode:         config.Mode,
		cloudID:      strings.TrimSpace(config.CloudID),
		email:        strings.TrimSpace(config.Email),
		apiToken:     strings.TrimSpace(config.APIToken),
		clientID:     strings.TrimSpace(config.ClientID),
		clientSecret: strings.TrimSpace(config.ClientSecret),
		options:      opts,
	}

	if site := strings.TrimSpace(config.SiteURL); site != "" {
		parsed, err := ParseSiteURL(site)
		if err != nil {
			return nil, err
		}
		c.site = parsed
	}

	switch config.Mode {
	case AuthBasic:
		if c.email == "" || c.apiToken == "" {
			return nil, fmt.Errorf("basic_auth requires both email and api_token")
		}
		if c.site == nil {
			return nil, fmt.Errorf("basic_auth requires site_url")
		}
	case AuthServiceAccount:
		if c.clientID == "" || c.clientSecret == "" {
			return nil, fmt.Errorf("service_account requires both client_id and client_secret")
		}
		if c.cloudID == "" && c.site == nil {
			return nil, fmt.Errorf("service_account requires cloud_id, or site_url to discover it from")
		}
	default:
		return nil, fmt.Errorf("confluence authentication mode is not set")
	}

	c.httpClient = newRetryClient(opts, config.HTTPClient, c.checkRetry)
	c.helperClient = newRetryClient(opts, config.HTTPClient, retryablehttp.DefaultRetryPolicy)
	switch c.mode {
	case AuthBasic:
		c.auth = &basicAuthenticator{email: c.email, apiToken: c.apiToken}
	case AuthServiceAccount:
		// clientcredentials.Config takes TokenURL as a string; this parse only
		// rejects a malformed endpoint here rather than at the first exchange.
		if _, err := url.Parse(opts.tokenURL); err != nil {
			return nil, fmt.Errorf("parse OAuth token endpoint: %w", err)
		}
		c.auth = &clientCredentialsAuthenticator{
			config: clientcredentials.Config{
				ClientID:     c.clientID,
				ClientSecret: c.clientSecret,
				TokenURL:     opts.tokenURL,
				AuthStyle:    oauth2.AuthStyleInParams,
			},
			httpClient: c.helperClient.StandardClient(),
		}
	}
	return c, nil
}

// Mode reports the authentication mode the client was configured with.
func (c *Client) Mode() AuthMode { return c.mode }

// SiteURL returns the configured site, or nil when only a cloud id was given.
func (c *Client) SiteURL() *url.URL {
	if c.site == nil {
		return nil
	}
	copied := *c.site
	return &copied
}

// CloudID returns the cloud id: the configured one, the discovered one once
// a request has resolved it, or "" while it is still unknown. It takes the
// lock because discovery writes the field from whichever goroutine gets there
// first.
func (c *Client) CloudID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cloudID
}

// ParseSiteURL normalizes a site value into an absolute https URL with no
// path. A bare host is accepted. Anything carrying a path, query, fragment or
// user information is rejected: the generated clients resolve their operation
// paths relative to the base URL, so a stray "/wiki" here would be silently
// concatenated into a URL that 404s.
func ParseSiteURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("site_url must not be empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("site_url is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("site_url scheme must be https or http, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("site_url must include a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("site_url must not include user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("site_url must not include a query or fragment")
	}
	if p := strings.TrimRight(parsed.Path, "/"); p != "" {
		return nil, fmt.Errorf("site_url must not include a path, got %q", parsed.Path)
	}
	return &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}, nil
}

// Prefix is the part of a Confluence URL that precedes "/wiki". It is the
// site itself under basic auth and the api.atlassian.com gateway route under
// a service account token.
func Prefix(mode AuthMode, site *url.URL, cloudID string) (*url.URL, error) {
	switch mode {
	case AuthBasic:
		if site == nil {
			return nil, fmt.Errorf("basic_auth requires site_url")
		}
		copied := *site
		copied.Path = ""
		return &copied, nil
	case AuthServiceAccount:
		if cloudID == "" {
			return nil, fmt.Errorf("service_account prefix requires a cloud id")
		}
		prefix, err := url.Parse(gatewayHost + "/ex/confluence/" + url.PathEscape(cloudID))
		if err != nil {
			return nil, fmt.Errorf("build gateway prefix for cloud id %q: %w", cloudID, err)
		}
		return prefix, nil
	default:
		return nil, fmt.Errorf("unknown authentication mode %s", mode)
	}
}

// BaseURLs derives the base URLs for the two generated clients from a prefix.
//
// The two specifications place the version prefix differently, so the clients
// are anchored differently: v2 operation paths are bare ("/spaces") because
// the v2 document carried "/wiki/api/v2" in its servers entry, while v1
// operation paths already include "/wiki/rest/api". Both bases end in a
// trailing slash because the generated clients resolve operation paths as
// relative references; without it the last segment of the base is silently
// replaced, dropping "v2" or the cloud id.
func BaseURLs(prefix *url.URL) (v1, v2 *url.URL) {
	base := *prefix
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawQuery, base.Fragment = "", ""

	v1URL := base
	v1URL.Path += "/"
	v2URL := base
	v2URL.Path += "/wiki/api/v2/"
	return &v1URL, &v2URL
}
