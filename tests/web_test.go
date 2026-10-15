package tests

import (
	"context"
	"io"
	. "lipbeta/script"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func webProgram(t *testing.T, source string, host Host) *Program {
	t.Helper()
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A real http.Server/http.Client connection over net.Pipe exercises framing
// without requiring socket permissions from the workspace sandbox.
type webPipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (l *webPipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *webPipeListener) Close() error { l.once.Do(func() { close(l.closed) }); return nil }
func (l *webPipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func TestHTTPWireProtocol(t *testing.T) {
	p := webProgram(t, `flow Main(request:object)->object {
 return if request.method == "POST" {{status:200,json:request.json}}
 else {{status:200,headers:{"Set-Cookie":["a=1","b=2"]},body:"hello"}}
}`, Host{})
	mux, err := NewHTTPMux([]HTTPRoute{{Pattern: "GET /", Program: p}, {Pattern: "HEAD /", Program: p}, {Pattern: "POST /", Program: p}}, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	listener := &webPipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
	server := &http.Server{Handler: mux}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		server.Close()
		if err := <-done; err != http.ErrServerClosed {
			t.Error(err)
		}
	}()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		client, conn := net.Pipe()
		select {
		case listener.connections <- conn:
			return client, nil
		case <-ctx.Done():
			client.Close()
			conn.Close()
			return nil, ctx.Err()
		case <-listener.closed:
			client.Close()
			conn.Close()
			return nil, net.ErrClosed
		}
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, method := range []string{"GET", "HEAD", "POST"} {
		r, err := http.NewRequest(method, "http://lip.test/", strings.NewReader(`{"text":"你好"}`))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatal(response.StatusCode, string(body), err)
		}
		switch method {
		case "GET":
			if string(body) != "hello" || len(response.Cookies()) != 2 {
				t.Fatal(response.Header, string(body))
			}
		case "HEAD":
			if len(body) != 0 || response.ContentLength != 5 {
				t.Fatal(string(body), response.Header)
			}
		case "POST":
			if string(body) != `{"text":"你好"}` || response.Header.Get("Content-Type") != "application/json" {
				t.Fatal(response.Header, string(body))
			}
		}
	}
}

func TestHTTPAuthorizationAndCompletedWriteOnResponseFault(t *testing.T) {
	type authorizedKey struct{}
	var calls atomic.Int64
	reports := make(chan Report, 2)
	host := Host{
		Authorize: func(ctx context.Context, request Request) error {
			if ctx.Value(authorizedKey{}) != true {
				return context.Canceled
			}
			return nil
		},
		Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "write"}, Version: "1", Result: Schema{Type: "null"}, Effect: WriteEffect,
			Invoke: func(context.Context, Request) Response { calls.Add(1); return Result(NullValue()) },
		}},
	}
	p := webProgram(t, `import host "app"
flow Main(request:object)->object {perform effect app.write();return {status:200,headers:{"X-Private":"secret","Bad\nHeader":"value"}}}`, host)
	handler, err := NewHTTPHandler(p, HTTPOptions{OnError: func(r *http.Request, report Report, err error) { reports <- report }})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	<-reports
	if w.Code != 403 || calls.Load() != 0 {
		t.Fatal(w.Code, calls.Load())
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), authorizedKey{}, true))
	handler.ServeHTTP(w, r)
	report := <-reports
	records := report.Journal.Records()
	if w.Code != 500 || calls.Load() != 1 || w.Header().Get("X-Private") != "" || len(records) != 1 || !records[0].Response.KnownCompleted {
		t.Fatal(w.Code, w.Header(), calls.Load(), records)
	}
}

func TestHTTPRoutesAndDataBoundary(t *testing.T) {
	p := webProgram(t, `flow Main(request:object)->object {return {status:200,json:request}}`, Host{})
	mux, err := NewHTTPMux([]HTTPRoute{{Pattern: "POST /items/{id}", Program: p}, {Pattern: "GET /files/{rest...}", Program: p}}, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/items/a%20b?tag=first&tag=second", strings.NewReader(`{"name":"Ada"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header["X-Test"] = []string{"one", "two"}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	data, err := DecodeData(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := data.Fields()
	if display(t, fields["params"]) != `{"id":"a b"}` || display(t, fields["query"]) != `{"tag":["first","second"]}` ||
		display(t, fields["json"]) != `{"name":"Ada"}` || !strings.Contains(display(t, fields["headers"]), `"x-test":["one","two"]`) {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/files/a/b", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"params":{"rest":"a/b"}`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("HEAD", "/files/a", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "GET" || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/files/a", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Fatal(w.Code, w.Header())
	}
}

func TestHTTPInvalidInputPrecedesActions(t *testing.T) {
	var calls atomic.Int64
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "write"}, Version: "1",
		Result: Schema{Type: "null"}, Effect: WriteEffect, Invoke: func(context.Context, Request) Response { calls.Add(1); return Result(NullValue()) },
	}}}
	p := webProgram(t, `import host "app"
flow Main(request:object)->object {perform effect app.write(); return {status:200,json:request.body}}`, host)
	handler, err := NewHTTPHandler(p, HTTPOptions{MaxBodyBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		body, query, contentType string
		status                   int
	}{
		{`{"x":1,"x":2}`, "", "application/json", 400},
		{`{"x":1} garbage`, "", "application/json", 400},
		{`{"x":"\ud800"}`, "", "application/json", 400},
		{`{"x":1e999}`, "", "application/json", 400},
		{`{"nested":[{"$lip.native":{}}]}`, "", "application/json", 400},
		{strings.Repeat("x", 65), "", "text/plain", 413},
		{"", "?x=%zz", "", 400},
		{"{}", "", "application/json; charset=latin1", 415},
		{"{}", "", "bad type", 415},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/"+tt.query, strings.NewReader(tt.body))
		r.Header.Set("Content-Type", tt.contentType)
		handler.ServeHTTP(w, r)
		if w.Code != tt.status || calls.Load() != 0 {
			t.Fatalf("%+v: %d %s calls=%d", tt, w.Code, w.Body.String(), calls.Load())
		}
	}
	w := httptest.NewRecorder()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/", nil).WithContext(ctx))
	if w.Code != 504 || calls.Load() != 0 {
		t.Fatal(w.Code, calls.Load())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(string([]byte{0xff, 0x00}))))
	if w.Code != 200 || w.Body.String() != `{"data":"/wA=","encoding":"base64"}` || calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), calls.Load())
	}
}

func TestHTTPResponseIsValidatedBeforeCommit(t *testing.T) {
	for _, response := range []string{
		`{status:200,headers:{"X-Leaked":"secret","Bad\nHeader":"value"},body:"secret"}`,
		`{status:200,headers:{"X-Leaked":"secret","X-Test":"a\r\nb"},body:"secret"}`,
		`{status:200,headers:{"Content-Length":"1"},body:"secret"}`,
		`{status:200,headers:{"x-test":"one","X-Test":"two"},body:"secret"}`,
		`{status:101,body:"secret"}`, `{status:200.5,body:"secret"}`, `{status:200,typo:1}`,
		`{status:200,json:1,body:"secret"}`, `{status:200,encoding:"base64"}`,
		`{status:200,body:"?",encoding:"base64"}`, `{status:204,body:"secret"}`,
		`{status:200,json:{"$lip.native":{}}}`, `{status:200,body:"too long"}`,
	} {
		p := webProgram(t, "flow Main(request:object)->object {return "+response+"}", Host{})
		handler, err := NewHTTPHandler(p, HTTPOptions{MaxResponseBytes: 4})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 500 || w.Header().Get("X-Leaked") != "" || w.Body.String() != `{"error":"internal_error"}` {
			t.Fatalf("%s: %d %v %s", response, w.Code, w.Header(), w.Body.String())
		}
	}
	p := webProgram(t, `flow Main(request:object)->object {return {status:200,headers:{"Set-Cookie":["a=1","b=2"]},body:"/wA=",encoding:"base64"}}`, Host{})
	handler, err := NewHTTPHandler(p, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || w.Body.String() != string([]byte{0xff, 0x00}) || len(w.Header().Values("Set-Cookie")) != 2 {
		t.Fatal(w.Code, w.Header(), w.Body.Bytes())
	}
}

func TestHTTPConcurrentRunsKeepIdentityAndClaimsSeparate(t *testing.T) {
	type identityKey struct{}
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var mu sync.Mutex
	runs := map[string]string{}
	counts := map[string]int{}
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "read"}, Version: "1",
		Params: []Parameter{{Name: "id", Schema: Schema{Type: "string"}}}, Result: Schema{Type: "string"}, Effect: ReadOnlyEffect, Concurrent: true,
		Invoke: func(ctx context.Context, r Request) Response {
			id, _ := r.Positional[0].Text()
			if ctx.Value(identityKey{}) != id {
				return Rejected("bad-context", "request identity was lost")
			}
			mu.Lock()
			if previous, ok := runs[r.CallID.RunID]; ok && previous != id {
				t.Error("run identity was shared")
			}
			runs[r.CallID.RunID], counts[r.CallID.RunID] = id, counts[r.CallID.RunID]+1
			mu.Unlock()
			started <- struct{}{}
			select {
			case <-release:
				return Result(r.Positional[0])
			case <-ctx.Done():
				return Uncertain(ctx.Err().Error())
			}
		},
	}}}
	p := webProgram(t, `import host "app"
flow Main(request:object)->object {
 facts=claim()
 for item in [1,2] parallel {
  result=perform effect app.read(request.query.id[0])
  facts <+ result
 }
 return {status:200,json:[row.value for row in facts]}
}`, host)
	handler, err := NewHTTPHandler(p, HTTPOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/?id="+id, nil)
			r = r.WithContext(context.WithValue(r.Context(), identityKey{}, id))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != `["`+id+`","`+id+`"]` {
				t.Errorf("%s: %d %s", id, w.Code, w.Body.String())
			}
		}(id)
	}
	for i := 0; i < 16; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("independent requests/reads did not overlap")
		}
	}
	close(release)
	wg.Wait()
	if len(runs) != 8 {
		t.Fatal(runs)
	}
	for _, count := range counts {
		if count != 2 {
			t.Fatal(counts)
		}
	}
}

func TestHTTPAdmissionCancellationAndUncertainNeverRetry(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{}, 2)
	reports := make(chan Report, 4)
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "write"}, Version: "1",
		Result: Schema{Type: "null"}, Effect: WriteEffect, Invoke: func(ctx context.Context, r Request) Response {
			calls.Add(1)
			entered <- struct{}{}
			<-ctx.Done()
			return Uncertain("write may have completed")
		},
	}}}
	p := webProgram(t, `import host "app"
flow Main(request:object)->object {perform effect app.write(); return {status:200}}`, host)
	handler, err := NewHTTPMux([]HTTPRoute{{Pattern: "POST /first", Program: p}, {Pattern: "POST /second", Program: p}}, HTTPOptions{Timeout: 5 * time.Second, MaxConcurrentRequests: 1,
		OnError: func(r *http.Request, report Report, err error) {
			if report.Journal != nil {
				reports <- report
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(first, httptest.NewRequest("POST", "/first", nil).WithContext(ctx))
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest("POST", "/second", nil))
	if second.Code != 503 || calls.Load() != 1 {
		t.Fatal(second.Code, calls.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not finish")
	}
	report := <-reports
	records := report.Journal.Records()
	if first.Code != 503 || calls.Load() != 1 || len(records) != 1 || records[0].Response.Outcome != UncertainOutcome {
		t.Fatal(first.Code, calls.Load(), records)
	}
	timed, err := NewHTTPHandler(p, HTTPOptions{Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	timed.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 504 || calls.Load() != 2 {
		t.Fatal(w.Code, calls.Load())
	}
}

func TestHTTPConfigurationRejectsInvalidRoutesAndSharedSessionState(t *testing.T) {
	p := webProgram(t, `flow Main(request:object)->object {return {status:204}}`, Host{})
	for _, invalid := range []*Program{nil, {}} {
		if _, err := NewHTTPHandler(invalid, HTTPOptions{}); err == nil {
			t.Fatal("accepted uninitialized program")
		}
	}
	for _, options := range []HTTPOptions{{Timeout: -1}, {Run: Options{RunID: "shared"}}, {Run: Options{Journal: NewMemoryJournal()}}} {
		if _, err := NewHTTPHandler(p, options); err == nil {
			t.Fatal("accepted unsafe options", options)
		}
	}
	for _, routes := range [][]HTTPRoute{nil, {{Pattern: "GET /{", Program: p}}, {{Pattern: "GET /", Program: p}, {Pattern: "GET /", Program: p}}} {
		if _, err := NewHTTPMux(routes, HTTPOptions{}); err == nil {
			t.Fatal("accepted invalid routes", routes)
		}
	}
	if _, err := NewHTTPHandler(webProgram(t, `flow Main()->number {return 1}`, Host{}), HTTPOptions{}); err == nil {
		t.Fatal("accepted invalid entry")
	}
	native := NewNativeHost(Host{})
	defer native.Close()
	if _, err := NewHTTPHandler(webProgram(t, `flow Main(request:object)->object {return {status:200}}`, native), HTTPOptions{}); err == nil {
		t.Fatal("accepted session-scoped native runtime")
	}
	handler, err := NewHTTPHandler(p, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", io.NopCloser(strings.NewReader(""))))
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHTTPExplicitMethodsAndEscapedPath(t *testing.T) {
	var calls atomic.Int64
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "write"}, Version: "1", Result: Schema{Type: "null"}, Effect: WriteEffect, Invoke: func(context.Context, Request) Response { calls.Add(1); return Result(NullValue()) }}}}
	p := webProgram(t, "import host \"app\"\nflow Main(request:object)->object{perform effect app.write();return {status:200,json:request}}", host)
	mux, err := NewHTTPMux([]HTTPRoute{{Pattern: "GET /{x}", Program: p}}, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"HEAD", "DELETE"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, "/a%2Fb", nil))
		if w.Code != 405 || w.Header().Get("Allow") != "GET" || calls.Load() != 0 {
			t.Fatal(method, w.Code, w.Header(), calls.Load())
		}
	}
	for _, target := range []string{"/a%2Fb", "http://example.test/a%2Fb"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"raw_path":"/a%2Fb"`) || !strings.Contains(w.Body.String(), `"params":{"x":"a/b"}`) || !strings.Contains(w.Body.String(), `"path":"/a/b"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	before := calls.Load()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("OPTIONS", "*", nil))
	if w.Code != 400 || calls.Load() != before {
		t.Fatal("asterisk target entered a run", w.Code, calls.Load())
	}
	if _, err := NewHTTPMux([]HTTPRoute{{Pattern: "/", Program: p}}, HTTPOptions{}); err == nil {
		t.Fatal("methodless route accepted")
	}
}
