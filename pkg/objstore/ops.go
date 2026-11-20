package objstore

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	objsdk "github.com/SolaTyolo/storage-api/packages/go-sdk"
)

// Entry is a folder or object for disk / FE listing UIs.
type Entry struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	IsDir        bool   `json:"is_dir"`
	Size         int64  `json:"size,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	CDNURL       string `json:"cdn_url,omitempty"`
	PublicURL    string `json:"public_url,omitempty"`
}

func (c *Client) Region() string {
	if c == nil {
		return ""
	}
	return c.region
}

// List returns one-level folders and objects under prefix (delimiter "/").
func (c *Client) List(ctx context.Context, prefix string) ([]Entry, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("object gateway not configured")
	}
	prefix, err := NormalizePrefix(prefix)
	if err != nil {
		return nil, err
	}
	var out []Entry
	token := ""
	for {
		page, err := c.sdk.List(ctx, objsdk.ListOptions{
			Prefix:            prefix,
			Delimiter:         "/",
			ContinuationToken: token,
			MaxKeys:           1000,
		})
		if err != nil {
			return nil, err
		}
		for _, e := range page.Entries {
			var name string
			if e.IsDir {
				name = strings.TrimSuffix(strings.TrimPrefix(e.Key, prefix), "/")
			} else {
				name = path.Base(e.Key)
			}
			if name == "" {
				continue
			}
			item := Entry{
				Key:   e.Key,
				Name:  name,
				IsDir: e.IsDir,
			}
			if !e.IsDir {
				item.Size = e.Size
				item.LastModified = e.LastModified
				item.CDNURL = e.CDNURL
				if item.CDNURL == "" {
					item.CDNURL = c.CDNURL(e.Key)
				}
				item.PublicURL = c.PublicURL(e.Key)
			}
			out = append(out, item)
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			break
		}
		token = page.NextContinuationToken
	}
	return out, nil
}

// Mkdir creates a folder marker via gateway upload.
func (c *Client) Mkdir(ctx context.Context, dir string) error {
	if !c.Configured() {
		return fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeDirKey(dir)
	if err != nil {
		return err
	}
	return c.sdk.Mkdir(ctx, strings.TrimSuffix(key, "/"), nil)
}

func (c *Client) GetText(ctx context.Context, key string) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return "", err
	}
	return c.sdk.GetText(ctx, key, nil)
}

func (c *Client) PutText(ctx context.Context, key, body string) error {
	if !c.Configured() {
		return fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return err
	}
	return c.sdk.PutText(ctx, key, body, nil)
}

// PutBytes uploads binary content via gateway presign + PUT.
func (c *Client) PutBytes(ctx context.Context, key string, body []byte, contentType string) (cdnURL string, err error) {
	if !c.Configured() {
		return "", fmt.Errorf("object gateway not configured")
	}
	key, err = NormalizeKey(key)
	if err != nil {
		return "", err
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	res, err := c.sdk.UploadBytes(ctx, body, objsdk.UploadOptions{
		Key:         key,
		ContentType: contentType,
	})
	if err != nil {
		return "", err
	}
	url := ""
	if res != nil {
		url = strings.TrimSpace(res.CDNURL)
	}
	if url == "" {
		url = c.CDNURL(key)
	}
	return url, nil
}

// GetBytes downloads object bytes via a short-lived presigned GET.
func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, string, error) {
	if !c.Configured() {
		return nil, "", fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return nil, "", err
	}
	res, err := c.sdk.Download(ctx, key, nil)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}
	ct := strings.TrimSpace(res.Header.Get("Content-Type"))
	return body, ct, nil
}

// PresignGet returns a short-lived download URL for key.
func (c *Client) PresignGet(ctx context.Context, key string, ttlSec int) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return "", err
	}
	opts := &objsdk.DownloadOptions{ExpireSec: ttlSec}
	if ttlSec <= 0 {
		opts = nil
	}
	return c.sdk.DownloadURL(ctx, key, opts)
}

func (c *Client) ListAllKeys(ctx context.Context, prefix string) ([]string, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("object gateway not configured")
	}
	prefix, err := NormalizePrefix(prefix)
	if err != nil {
		return nil, err
	}
	return c.sdk.ListAllKeys(ctx, prefix, nil)
}

func (c *Client) SyncPrefix(ctx context.Context, srcPrefix, destPrefix string) error {
	if !c.Configured() {
		return fmt.Errorf("object gateway not configured")
	}
	_, err := c.sdk.SyncPrefix(ctx, srcPrefix, destPrefix, &objsdk.SyncOptions{
		DeleteOrphans: true,
	})
	return err
}

func (c *Client) HeadExists(ctx context.Context, key string) (bool, error) {
	if !c.Configured() {
		return false, fmt.Errorf("object gateway not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return false, err
	}
	return c.sdk.HeadExists(ctx, key, nil)
}

func NormalizePrefix(prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.ReplaceAll(prefix, "\\", "/")
	prefix = strings.TrimLeft(prefix, "/")
	if prefix == "" {
		return "", nil
	}
	if strings.Contains(prefix, "..") {
		return "", fmt.Errorf("invalid prefix")
	}
	cleaned := path.Clean("/" + prefix)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "", nil
	}
	if !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned, nil
}

func NormalizeDirKey(dir string) (string, error) {
	prefix, err := NormalizePrefix(dir)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return "", fmt.Errorf("empty folder path")
	}
	return prefix, nil
}
