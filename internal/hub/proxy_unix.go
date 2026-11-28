//go:build !windows

package hub

import (
	"net/http"
	"net/url"
)

func systemProxyForRequest(req *http.Request) (*url.URL, error) {
	return currentSystemProxy().forURL(req.URL), nil
}
