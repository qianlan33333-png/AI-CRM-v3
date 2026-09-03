package main

import (
	"errors"
	"net/http"
	"strings"
)

// mountSegmentAPI keeps the established router signature stable while Segment
// is introduced. It owns only its canonical prefix and delegates everything
// else to the existing application handler.
func mountSegmentAPI(next, audience http.Handler) (http.Handler, error) {
	if next == nil || audience == nil {
		return nil, errors.New("segment route dependencies are required")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/admin/ai-audience/") {
			audience.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}

func mountSegmentWebhook(next, webhook http.Handler) (http.Handler, error) {
	if next == nil || webhook == nil {
		return nil, errors.New("segment webhook route dependencies are required")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/integrations/ai-audience/") {
			webhook.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}
