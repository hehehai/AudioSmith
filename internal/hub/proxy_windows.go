//go:build windows

package hub

import (
	"net/http"
	"net/url"

	"github.com/egoist/mygo/netproxy"
)

func systemProxyForRequest(req *http.Request) (*url.URL, error) {
	return netproxy.Proxy(req)
}
