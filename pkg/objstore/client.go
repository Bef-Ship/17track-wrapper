package objstore

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	objsdk "github.com/SolaTyolo/storage-api/packages/go-sdk"
)

// Config wires a bucket-scoped client against obj-gateway.
type Config struct {
	// Endpoint is the internal gateway base, e.g. http://obj-gateway:8080/api/v1
	Endpoint string
	// PublicEndpoint is returned to browsers as gateway_endpoint.
	// Remote admin/app SPAs on CDN must use an absolute API URL
	// (e.g. https://develop-api.example.com/obj/api/v1). Relative paths resolve
	// against the SPA origin and hit COS (MethodNotAllowed on /presign/put).
	PublicEndpoint string
	// JWTSecret signs scoped upload tokens (must match gateway JWT_SECRET).
	JWTSecret string
	// ServiceToken is an optional pre-minted service_role JWT; if empty, one is minted from JWTSecret.
	ServiceToken string
	Bucket       string
	Region       string
	// PublicBase is the object public URL prefix (scheme://host[/bucket]), no trailing slash.
	PublicBase string
	// CDNBase is optional CDN origin for display URLs (no trailing slash).
	CDNBase string
}

// Client issues scoped gateway sessions and wraps the Go SDK for server-side I/O.
type Client struct {
	sdk            *objsdk.Client
	endpoint       string
	publicEndpoint string
	jwtSecret      []byte
	bucket         string
	region         string
	publicBase     string
	cdnBase        string
}

// New builds a Client. Empty Endpoint/Bucket yields an unconfigured client (Configured=false).
func New(cfg Config) (*Client, error) {
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.PublicEndpoint = strings.TrimRight(strings.TrimSpace(cfg.PublicEndpoint), "/")
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.JWTSecret = strings.TrimSpace(cfg.JWTSecret)
	cfg.ServiceToken = strings.TrimSpace(cfg.ServiceToken)
	cfg.PublicBase = strings.TrimRight(strings.TrimSpace(cfg.PublicBase), "/")
	cfg.CDNBase = strings.TrimRight(strings.TrimSpace(cfg.CDNBase), "/")
	cfg.Region = strings.TrimSpace(cfg.Region)
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	c := &Client{
		endpoint:       cfg.Endpoint,
		publicEndpoint: cfg.PublicEndpoint,
		jwtSecret:      []byte(cfg.JWTSecret),
		bucket:         cfg.Bucket,
		region:         cfg.Region,
		publicBase:     cfg.PublicBase,
		cdnBase:        cfg.CDNBase,
	}
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return c, nil
	}
	if cfg.PublicEndpoint == "" {
		c.publicEndpoint = cfg.Endpoint
	}

	token := cfg.ServiceToken
	if token == "" {
		if cfg.JWTSecret == "" {
			return nil, fmt.Errorf("objstore: JWTSecret or ServiceToken required")
		}
		var err error
		token, err = mintJWT(cfg.JWTSecret, claims{
			Role: "service_role",
		}, 24*time.Hour)
		if err != nil {
			return nil, err
		}
	}

	sdk, err := objsdk.New(objsdk.Config{
		Endpoint:      cfg.Endpoint,
		Token:         token,
		DefaultBucket: cfg.Bucket,
	})
	if err != nil {
		return nil, err
	}
	c.sdk = sdk
	return c, nil
}

func (c *Client) Configured() bool {
	return c != nil && c.sdk != nil && c.bucket != "" && c.publicEndpoint != ""
}

func (c *Client) Bucket() string {
	if c == nil {
		return ""
	}
	return c.bucket
}

func (c *Client) SDK() *objsdk.Client {
	if c == nil {
		return nil
	}
	return c.sdk
}

// UploadSession is returned to browsers so they can upload via @obj-gateway/sdk.
type UploadSession struct {
	GatewayEndpoint string `json:"gateway_endpoint"`
	Token           string `json:"token"`
	Bucket          string `json:"bucket"`
	Key             string `json:"key"`
	ContentType     string `json:"content_type"`
	ExpiresIn       int    `json:"expires_in"`
	PublicURL       string `json:"public_url"`
	CDNURL          string `json:"cdn_url"`
}

type claims struct {
	jwt.RegisteredClaims
	Bucket          string   `json:"bucket,omitempty"`
	AllowedPrefixes []string `json:"allowedPrefixes,omitempty"`
	Role            string   `json:"role,omitempty"`
}

// IssueUploadSession mints a scoped JWT for key prefix access and returns browser credentials.
func (c *Client) IssueUploadSession(key, contentType string, ttl time.Duration) (UploadSession, error) {
	if !c.Configured() {
		return UploadSession{}, fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return UploadSession{}, err
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if len(c.jwtSecret) == 0 {
		return UploadSession{}, fmt.Errorf("object gateway JWT secret not configured")
	}

	prefix := path.Dir(key)
	if prefix == "." {
		prefix = ""
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	tok, err := mintJWT(string(c.jwtSecret), claims{
		Bucket:          c.bucket,
		AllowedPrefixes: []string{prefix},
	}, ttl)
	if err != nil {
		return UploadSession{}, err
	}

	return UploadSession{
		GatewayEndpoint: c.publicEndpoint,
		Token:           tok,
		Bucket:          c.bucket,
		Key:             key,
		ContentType:     contentType,
		ExpiresIn:       int(ttl.Seconds()),
		PublicURL:       c.PublicURL(key),
		CDNURL:          c.CDNURL(key),
	}, nil
}

// PresignPut asks the gateway for a PUT URL (server-side helper).
func (c *Client) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (*objsdk.PresignPutResponse, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	expireSec := int(ttl.Seconds())
	if expireSec <= 0 {
		expireSec = 900
	}
	return c.sdk.PresignPut(ctx, objsdk.UploadOptions{
		Key:         key,
		ContentType: contentType,
		ExpireSec:   expireSec,
	})
}

func (c *Client) PublicURL(key string) string {
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if c == nil || c.publicBase == "" {
		return ""
	}
	return c.publicBase + "/" + key
}

func (c *Client) CDNURL(key string) string {
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if c != nil && c.cdnBase != "" {
		return c.cdnBase + "/" + key
	}
	return c.PublicURL(key)
}

func (c *Client) IsOwnedPublicURL(raw string) bool {
	if c == nil {
		return false
	}
	raw = strings.TrimSpace(raw)
	prefixes := []string{}
	if c.publicBase != "" {
		prefixes = append(prefixes, strings.TrimRight(c.publicBase, "/")+"/")
	}
	if c.cdnBase != "" {
		prefixes = append(prefixes, c.cdnBase+"/")
	}
	for _, p := range prefixes {
		if p != "/" && strings.HasPrefix(raw, p) {
			return true
		}
	}
	return false
}

// ObjectKeyFromURL extracts an object key that begins with marker from a public/CDN URL.
// Handles CDN paths that include an extra prefix (e.g. /cdn/<bucket>/public/...).
func ObjectKeyFromURL(raw, marker string) (string, bool) {
	raw = strings.TrimSpace(raw)
	marker = strings.TrimLeft(strings.TrimSpace(marker), "/")
	if raw == "" || marker == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	p := strings.TrimPrefix(u.Path, "/")
	idx := strings.Index(p, marker)
	if idx < 0 {
		return "", false
	}
	key := p[idx:]
	if key == "" || strings.Contains(key, "..") {
		return "", false
	}
	return key, true
}

// HeadCDNURL returns the gateway CDN URL for an existing object (falls back to CDNURL(key)).
func (c *Client) HeadCDNURL(ctx context.Context, key string) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return "", err
	}
	head, err := c.sdk.Head(ctx, key, nil)
	if err != nil {
		return "", err
	}
	if head == nil {
		return "", fmt.Errorf("object not found")
	}
	cdn := strings.TrimSpace(head.CDNURL)
	if cdn == "" {
		cdn = c.CDNURL(key)
	}
	return cdn, nil
}

func mintJWT(secret string, c claims, ttl time.Duration) (string, error) {
	now := time.Now()
	c.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString([]byte(secret))
}

// BuildPublicBase builds a path-style or virtual-hosted public object URL base.
func BuildPublicBase(endpoint, bucket string, pathStyle bool) (string, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	bucket = strings.TrimSpace(bucket)
	if endpoint == "" || bucket == "" {
		return "", fmt.Errorf("missing endpoint or bucket")
	}
	if pathStyle {
		return endpoint + "/" + bucket, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	u.Host = bucket + "." + u.Host
	return strings.TrimRight(u.String(), "/"), nil
}

func NormalizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, "\\", "/")
	key = strings.TrimLeft(key, "/")
	if key == "" {
		return "", fmt.Errorf("empty object key")
	}
	if strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid object key")
	}
	cleaned := path.Clean("/" + key)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("empty object key")
	}
	return cleaned, nil
}
