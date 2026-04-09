//go:build tinygo

package backend

import "net/http"

func DefaultHTTPClient() *http.Client {
	return &http.Client{}
}
