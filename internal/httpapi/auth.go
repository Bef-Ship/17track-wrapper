package httpapi

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// TokenHeader is the 17TRACK auth header name; callers may also send
// "Authorization: Bearer <token>".
const TokenHeader = "17token"

// ServiceTokens maps each calling service's name to its token, so services are
// identified and rotated / revoked independently.
type ServiceTokens map[string]string

// ParseServiceTokens parses "backend:tok1,gateway:tok2". An entry without a name
// ("tok") is registered as "default".
func ParseServiceTokens(spec string) (ServiceTokens, error) {
	out := ServiceTokens{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, tok := "default", part
		if i := strings.IndexByte(part, ':'); i >= 0 {
			name, tok = strings.TrimSpace(part[:i]), strings.TrimSpace(part[i+1:])
		}
		if name == "" || tok == "" {
			return nil, fmt.Errorf("service token %q: want name:token", part)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("service token %q: duplicate name", name)
		}
		out[name] = tok
	}
	return out, nil
}

// match returns the service owning tok. Every entry is compared so timing does not
// reveal which name matched.
func (t ServiceTokens) match(tok string) (string, bool) {
	found := ""
	for name, want := range t {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1 {
			found = name
		}
	}
	return found, found != ""
}

func requestToken(r *http.Request) string {
	if tok := strings.TrimSpace(r.Header.Get(TokenHeader)); tok != "" {
		return tok
	}
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

type callerKey struct{}

// Caller returns the authenticated service name ("" when auth is disabled).
func Caller(ctx context.Context) string {
	name, _ := ctx.Value(callerKey{}).(string)
	return name
}
