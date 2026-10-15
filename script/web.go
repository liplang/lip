package script

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// HTTPOptions bounds one request and its Flow run. Zero values select finite
// defaults. Programs and explicit Host contracts are shared; journals and run
// identities are always fresh. OnError is for server-side diagnostics only.
type HTTPOptions struct {
	Run                   Options
	Timeout               time.Duration
	MaxBodyBytes          int64
	MaxDataBytes          int64
	MaxResponseBytes      int64
	MaxConcurrentRequests int
	OnError               func(*http.Request, Report, error)
}

type HTTPRoute struct {
	Pattern string // Go ServeMux pattern, such as "GET /users/{id}".
	Program *Program
}

func normalizeHTTPOptions(o HTTPOptions) (HTTPOptions, error) {
	if o.Timeout < 0 || o.MaxDataBytes < 0 || o.MaxBodyBytes < 0 || o.MaxResponseBytes < 0 || o.MaxConcurrentRequests < 0 ||
		o.Run.Parallelism < 0 || o.Run.MaxSteps < 0 || o.Run.MaxElements < 0 || o.Run.MaxNodes < 0 || o.Run.MaxFrames < 0 {
		return o, diagnostic("E-HTTP", "HTTP limits must be nonnegative")
	}
	if o.Run.RunID != "" || o.Run.Journal != nil {
		return o, diagnostic("E-HTTP", "HTTP requests require fresh run identities and journals")
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	if o.MaxDataBytes == 0 {
		o.MaxDataBytes = 8 << 20
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = 1 << 20
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 1 << 20
	}
	if o.MaxConcurrentRequests == 0 {
		o.MaxConcurrentRequests = 64
	}
	if o.Run.Parallelism == 0 {
		o.Run.Parallelism = 4
	}
	if o.Run.MaxSteps == 0 {
		o.Run.MaxSteps = 1000000
	}
	if o.Run.MaxElements == 0 {
		o.Run.MaxElements = 100000
	}
	if o.Run.MaxNodes == 0 {
		o.Run.MaxNodes = 100000
	}
	if o.Run.MaxFrames == 0 {
		o.Run.MaxFrames = DefaultMaxFrames
	}
	o.Run.After = append([]After(nil), o.Run.After...)
	return o, nil
}

func validateHTTPProgram(p *Program) error {
	if p == nil || p.syntax == nil || len(p.syntax.params) != 1 || p.syntax.params[0].name != "request" ||
		p.syntax.params[0].typ != "object" || p.syntax.result != "object" {
		return diagnostic("E-HTTP", "HTTP entry must be flow Name(request:object)->object")
	}
	// Automatic bridges retain session-scoped objects and workers. The HTTP
	// profile deliberately uses explicit contracts until request-scoped native
	// runtime ownership is implemented; accepting a shared Session is unsafe.
	if p.host.Native != nil {
		return diagnostic("E-HTTP", "HTTP programs require explicit Host contracts; session-scoped native bridges are not supported")
	}
	return nil
}

// NewHTTPHandler adapts a precompiled Flow without changing core semantics.
// It does not own or close the application's Host or database connection pool.
func NewHTTPHandler(p *Program, options HTTPOptions) (http.Handler, error) {
	o, err := normalizeHTTPOptions(options)
	if err != nil {
		return nil, err
	}
	if err := validateHTTPProgram(p); err != nil {
		return nil, err
	}
	return newHTTPHandler(p, o, make(chan struct{}, o.MaxConcurrentRequests)), nil
}

// NewHTTPMux registers all routes before serving. Its request admission limit
// is shared across routes; invalid and conflicting patterns fail at startup.
func NewHTTPMux(routes []HTTPRoute, options HTTPOptions) (handler http.Handler, err error) {
	o, err := normalizeHTTPOptions(options)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, diagnostic("E-HTTP", "HTTP mux requires at least one route")
	}
	defer func() {
		if failure := recover(); failure != nil {
			handler, err = nil, diagnostic("E-HTTP", fmt.Sprintf("invalid HTTP route: %v", failure))
		}
	}()
	muxes := map[string]*http.ServeMux{}
	slots := make(chan struct{}, o.MaxConcurrentRequests)
	for _, route := range routes {
		if err := validateHTTPProgram(route.Program); err != nil {
			return nil, err
		}
		method, pattern, explicit := strings.Cut(route.Pattern, " ")
		if !explicit || !httpHeaderName(method) || pattern == "" || strings.ContainsAny(pattern, " \t\r\n") {
			return nil, diagnostic("E-HTTP", "routes require an explicit HTTP method and path pattern")
		}
		mux := muxes[method]
		if mux == nil {
			mux = http.NewServeMux()
			muxes[method] = mux
		}
		mux.Handle(pattern, newHTTPHandler(route.Program, o, slots))
	}
	methods := make([]string, 0, len(muxes))
	for method := range muxes {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, err := normalizeHTTPRequest(r)
		if err != nil {
			http.Error(w, "invalid_request", http.StatusBadRequest)
			return
		}
		r = request
		if mux := muxes[r.Method]; mux != nil {
			if _, pattern := mux.Handler(r); pattern != "" {
				mux.ServeHTTP(w, r)
				return
			}
		}
		allowed := []string{}
		for _, method := range methods {
			if _, pattern := muxes[method].Handler(r); pattern != "" {
				allowed = append(allowed, method)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		http.NotFound(w, r)
	}), nil
}
func normalizeHTTPRequest(r *http.Request) (*http.Request, error) {
	if r == nil || r.URL == nil || r.RequestURI == "*" || r.URL.Path == "*" || r.URL.Opaque != "" || r.URL.User != nil {
		return nil, diagnostic("E-HTTP", "unsupported request target")
	}
	if !r.URL.IsAbs() && r.RequestURI != "" && !strings.HasPrefix(r.RequestURI, "/") {
		return nil, diagnostic("E-HTTP", "unsupported request target")
	}
	if r.URL.IsAbs() && r.URL.Host == "" {
		return nil, diagnostic("E-HTTP", "absolute request target requires authority")
	}
	copy := *r
	url := *r.URL
	copy.URL = &url
	if url.IsAbs() {
		copy.Host = url.Host
	}
	if url.Path == "" {
		copy.URL.Path = "/"
	}
	return &copy, nil
}

func newHTTPHandler(p *Program, o HTTPOptions, slots chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestTarget, err := normalizeHTTPRequest(r)
		if err != nil {
			http.Error(w, "invalid_request", http.StatusBadRequest)
			return
		}
		r = requestTarget
		if r.Body != nil {
			defer r.Body.Close()
		}
		ctx, cancel := context.WithTimeout(r.Context(), o.Timeout)
		defer cancel()
		failRequest := func(status int, code string, report Report, err error) {
			if o.OnError != nil {
				o.OnError(r, report, err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if r.Method != http.MethodHead {
				fmt.Fprintf(w, "{\"error\":%q}", code)
			}
		}
		if ctx.Err() != nil {
			status, code := http.StatusServiceUnavailable, "request_cancelled"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status, code = http.StatusGatewayTimeout, "request_timeout"
			}
			failRequest(status, code, Report{}, ctx.Err())
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			failRequest(http.StatusServiceUnavailable, "server_busy", Report{}, diagnostic("E-HTTP", "request capacity exhausted"))
			return
		}
		request, status, err := httpRequestData(w, r, o.MaxBodyBytes)
		if err != nil {
			failRequest(status, "invalid_request", Report{}, err)
			return
		}
		input := object(map[string]Value{"positional": ListValue(request), "named": object(nil)})
		encoded, err := Canonical(input)
		if err != nil || int64(len(encoded)) > o.MaxDataBytes {
			failRequest(http.StatusBadRequest, "invalid_request", Report{}, diagnostic("E-HTTP", "request Data budget exhausted"))
			return
		}
		report, err := p.RunEntry(ctx, input, o.Run)
		if err != nil {
			status, code := http.StatusInternalServerError, "internal_error"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status, code = http.StatusGatewayTimeout, "request_timeout"
			} else if errors.Is(ctx.Err(), context.Canceled) {
				status, code = http.StatusServiceUnavailable, "request_cancelled"
			} else if report.Error != nil && report.Error.Code == "E-CAPABILITY" {
				status, code = http.StatusForbidden, "forbidden"
			}
			failRequest(status, code, report, err)
			return
		}
		result := report.EntryOutput.o["value"]
		encoded, err = Canonical(result)
		if err != nil || int64(len(encoded)) > o.MaxDataBytes {
			failRequest(http.StatusInternalServerError, "internal_error", report, diagnostic("E-HTTP", "response Data budget exhausted"))
			return
		}
		response, err := httpResponseData(result, o.MaxResponseBytes)
		if err != nil {
			failRequest(http.StatusInternalServerError, "internal_error", report, err)
			return
		}
		if err := ctx.Err(); err != nil {
			status, code := http.StatusServiceUnavailable, "request_cancelled"
			if errors.Is(err, context.DeadlineExceeded) {
				status, code = http.StatusGatewayTimeout, "request_timeout"
			}
			failRequest(status, code, report, err)
			return
		}
		for key, values := range response.headers {
			w.Header()[key] = values
		}
		if response.status != http.StatusNoContent && response.status != http.StatusNotModified {
			w.Header().Set("Content-Length", strconv.Itoa(len(response.body)))
		}
		w.WriteHeader(response.status)
		if r.Method != http.MethodHead && len(response.body) != 0 {
			if _, err := w.Write(response.body); err != nil && o.OnError != nil {
				o.OnError(r, report, err)
			}
		}
	})
}

// Transport bytes use explicit UTF-8/base64 encoding; JSON uses the same strict
// Data decoder as Flow inputs. No HTTP parsing operation invokes a provider.
func httpRequestData(w http.ResponseWriter, r *http.Request, limit int64) (Value, int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return Value{}, http.StatusBadRequest, err
	}
	headers := map[string][]string{}
	for key, values := range r.Header {
		headers[strings.ToLower(key)] = append(headers[strings.ToLower(key)], values...)
	}
	params := map[string]string{}
	for _, segment := range strings.Split(r.Pattern, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") && segment != "{$}" {
			name := strings.TrimSuffix(segment[1:len(segment)-1], "...")
			params[name] = r.PathValue(name)
		}
	}
	var raw []byte
	if r.Body != nil {
		reader := http.MaxBytesReader(w, r.Body, limit)
		raw, err = io.ReadAll(reader)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return Value{}, http.StatusRequestEntityTooLarge, err
			}
			return Value{}, http.StatusBadRequest, err
		}
	}
	body, encoding := string(raw), "utf-8"
	if !utf8.Valid(raw) {
		body, encoding = base64.StdEncoding.EncodeToString(raw), "base64"
	}
	input, err := FromNative(map[string]any{
		"method": r.Method, "path": r.URL.Path, "raw_path": r.URL.EscapedPath(), "host": r.Host, "params": params,
		"query": map[string][]string(query), "headers": headers,
		"body": map[string]string{"encoding": encoding, "data": body}, "json": nil,
	})
	if err != nil {
		return Value{}, http.StatusBadRequest, err
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, attributes, err := mime.ParseMediaType(contentType)
		if err != nil {
			return Value{}, http.StatusUnsupportedMediaType, err
		}
		if mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json") {
			if charset := attributes["charset"]; charset != "" && !strings.EqualFold(charset, "utf-8") {
				return Value{}, http.StatusUnsupportedMediaType, diagnostic("E-HTTP", "JSON charset must be UTF-8")
			}
			if len(raw) != 0 {
				json, err := DecodeData(raw)
				if err != nil {
					return Value{}, http.StatusBadRequest, err
				}
				if httpHasNativeReference(json) {
					return Value{}, http.StatusBadRequest, diagnostic("E-HTTP", "native references cannot enter through HTTP JSON")
				}
				input.o["json"] = json
			}
		}
	}
	return input, 0, nil
}

func httpHasNativeReference(v Value) bool {
	if _, present := v.o[nativeReferenceKey]; present {
		return true
	}
	for _, x := range v.o {
		if httpHasNativeReference(x) {
			return true
		}
	}
	for _, x := range v.l {
		if httpHasNativeReference(x) {
			return true
		}
	}
	return false
}

type httpResponse struct {
	status  int
	headers http.Header
	body    []byte
}

func httpResponseData(v Value, limit int64) (httpResponse, error) {
	failure := func(message string) (httpResponse, error) { return httpResponse{}, diagnostic("E-HTTP", message) }
	if v.k != Object || !v.IsData() {
		return failure("HTTP response must be a Data object")
	}
	for key := range v.o {
		if key != "status" && key != "headers" && key != "json" && key != "body" && key != "encoding" {
			return failure("unknown HTTP response field " + key)
		}
	}
	status, present := v.o["status"]
	if !present || status.k != Number || status.n != math.Trunc(status.n) || status.n < 200 || status.n > 599 {
		return failure("HTTP status must be an integer from 200 to 599")
	}
	response := httpResponse{status: int(status.n), headers: http.Header{}}
	if headers, present := v.o["headers"]; present {
		if headers.k != Object {
			return failure("HTTP headers must be an object")
		}
		for key, value := range headers.o {
			if !httpHeaderName(key) {
				return failure("invalid HTTP response header name")
			}
			canonical := http.CanonicalHeaderKey(key)
			if _, duplicate := response.headers[canonical]; duplicate {
				return failure("duplicate case-insensitive HTTP response header")
			}
			switch canonical {
			case "Connection", "Content-Length", "Transfer-Encoding", "Trailer", "Upgrade", "Keep-Alive", "Proxy-Connection", "Te":
				return failure("HTTP transport header is controlled by the Host")
			}
			values := []Value{value}
			if value.k == List {
				values = value.l
			}
			texts := []string{}
			for _, item := range values {
				if item.k != String || !httpHeaderValue(item.s) {
					return failure("invalid HTTP response header value")
				}
				texts = append(texts, item.s)
			}
			response.headers[canonical] = texts
		}
	}
	json, hasJSON := v.o["json"]
	body, hasBody := v.o["body"]
	encoding, hasEncoding := v.o["encoding"]
	if hasJSON && hasBody || hasEncoding && !hasBody {
		return failure("HTTP response selects either json or body; encoding requires body")
	}
	if hasJSON {
		if httpHasNativeReference(json) {
			return failure("native references cannot leave through HTTP JSON")
		}
		var err error
		response.body, err = EncodeJSON(json)
		if err != nil {
			return httpResponse{}, err
		}
		if response.headers.Get("Content-Type") == "" {
			response.headers.Set("Content-Type", "application/json")
		}
	} else if hasBody {
		if body.k != String {
			return failure("HTTP body must be a string")
		}
		codec := "utf-8"
		if hasEncoding {
			if encoding.k != String {
				return failure("HTTP encoding must be a string")
			}
			codec = encoding.s
		}
		switch codec {
		case "utf-8":
			response.body = []byte(body.s)
			if response.headers.Get("Content-Type") == "" {
				response.headers.Set("Content-Type", "text/plain; charset=utf-8")
			}
		case "base64":
			var err error
			response.body, err = base64.StdEncoding.Strict().DecodeString(body.s)
			if err != nil {
				return failure("invalid base64 HTTP body")
			}
			if response.headers.Get("Content-Type") == "" {
				response.headers.Set("Content-Type", "application/octet-stream")
			}
		default:
			return failure("HTTP encoding must be utf-8 or base64")
		}
	}
	if int64(len(response.body)) > limit {
		return failure("HTTP response body exceeds its budget")
	}
	if (response.status == http.StatusNoContent || response.status == http.StatusNotModified || response.status == http.StatusResetContent) && len(response.body) != 0 {
		return failure("HTTP status forbids a response body")
	}
	return response, nil
}

func httpHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !asciiLetter(c) && !asciiDigit(c) && !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			return false
		}
	}
	return true
}
func httpHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 32 && value[i] != '\t' || value[i] == 127 {
			return false
		}
	}
	return true
}
