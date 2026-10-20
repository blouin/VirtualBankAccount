// Package api serves the routes in spec/auth.yaml and spec/operations.yaml.
// spec/admin_family.yaml and spec/admin_operator.yaml are not implemented (the seed-poc job replaces them).
package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

type Request = events.LambdaFunctionURLRequest
type Response = events.LambdaFunctionURLResponse

const pathPrefix = "/api/v1" // the servers url in every spec

// Handler returns the web request handler. Requests must carry the x-origin-secret header that CloudFront adds
// (iac/main.tf), so the public Function URL can't be called directly; an empty secret rejects everything.
func Handler(db *sql.DB, originSecret string) func(context.Context, Request) (Response, error) {
	mux := NewMux(db)
	return func(ctx context.Context, req Request) (Response, error) {
		if got := req.Headers["x-origin-secret"]; originSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(originSecret)) != 1 {
			return jsonResponse(403, map[string]string{"message": "forbidden"}), nil
		}
		path, ok := strings.CutPrefix(req.RawPath, pathPrefix)
		if !ok {
			return jsonResponse(404, map[string]string{"message": "not found"}), nil
		}
		body := []byte(req.Body)
		if req.IsBase64Encoded {
			var err error
			if body, err = base64.StdEncoding.DecodeString(req.Body); err != nil {
				return jsonResponse(400, map[string]string{"message": "invalid body"}), nil
			}
		}
		url := path
		if req.RawQueryString != "" {
			url += "?" + req.RawQueryString
		}
		r, err := http.NewRequestWithContext(ctx, req.RequestContext.HTTP.Method, url, bytes.NewReader(body))
		if err != nil {
			return jsonResponse(400, map[string]string{"message": "bad request"}), nil
		}
		for k, v := range req.Headers {
			r.Header.Set(k, v)
		}
		r.RemoteAddr = ClientIP(req)
		rec := &recorder{h: http.Header{}, status: 200}
		mux.ServeHTTP(rec, r)
		headers := map[string]string{}
		for k, v := range rec.h {
			headers[strings.ToLower(k)] = v[0]
		}
		return Response{StatusCode: rec.status, Headers: headers, Body: rec.buf.String()}, nil
	}
}

// recorder is the smallest http.ResponseWriter that can fill a Lambda response.
type recorder struct {
	h      http.Header
	status int
	buf    bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.h }
func (r *recorder) WriteHeader(code int)        { r.status = code }
func (r *recorder) Write(b []byte) (int, error) { return r.buf.Write(b) }

// ClientIP is the viewer's address. The sourceIp is CloudFront's edge, which Lambda may append to
// x-forwarded-for after CloudFront appended the viewer; so drop the edge, then take the rightmost entry
// (anything left of it is client-supplied and spoofable). Only safe behind the origin-secret check.
func ClientIP(req Request) string {
	edge := req.RequestContext.HTTP.SourceIP
	parts := strings.Split(req.Headers["x-forwarded-for"], ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if ip != "" && ip != edge {
			return ip
		}
	}
	return edge
}
