package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"lip/script"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func serve(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:8080", "HTTP listen address")
	pattern := flags.String("route", "GET /{$}", "Go ServeMux pattern for the single source file")
	entry := flags.String("entry", "", "root Flow for a single source")
	project := flags.String("project", "", "project root for static LIP imports")
	manifest := flags.String("routes", "", "JSON array of {pattern, source, entry?}; paths relative to the manifest")
	hostName := flags.String("host", "", "explicit Host contracts: empty or demo; no automatic native bridge")
	timeout := flags.Duration("timeout", 30*time.Second, "per-request execution timeout")
	concurrent := flags.Int("concurrent", 64, "maximum requests across all routes")
	bodyLimit := flags.Int64("body-bytes", 1<<20, "maximum request body bytes")
	responseLimit := flags.Int64("response-bytes", 1<<20, "maximum encoded response body bytes")
	parallel := flags.Int("parallel", 4, "maximum active leaf evaluations per request (waiting containers yield)")
	steps := flags.Int64("steps", 1000000, "step budget per request")
	elements := flags.Int64("elements", 100000, "list budget per request")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var routes []httpSourceRoute
	if *manifest != "" {
		if flags.NArg() != 0 {
			return fmt.Errorf("serve -routes does not accept a source argument")
		}
		var hasPattern bool
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "route" {
				hasPattern = true
			}
		})
		if hasPattern {
			return fmt.Errorf("serve selects either -route or -routes")
		}
		var err error
		routes, err = readHTTPRoutes(*manifest)
		if err != nil {
			return err
		}
	} else {
		if flags.NArg() != 1 {
			return fmt.Errorf("serve requires one source file or -routes manifest")
		}
		routes = []httpSourceRoute{{pattern: *pattern, source: flags.Arg(0), entry: *entry}}
	}
	host := script.Host{}
	if *hostName == "demo" {
		var err error
		host, err = script.DemoHost()
		if err != nil {
			return err
		}
	} else if *hostName != "" {
		return fmt.Errorf("unknown Host %q", *hostName)
	}
	defer host.Close()
	programs := map[string]*script.Program{}
	compiled := []script.HTTPRoute{}
	for _, route := range routes {
		path, err := filepath.Abs(route.source)
		if err != nil {
			return err
		}
		key := path + "\x00" + route.entry
		program := programs[key]
		if program == nil {
			sources, err := script.LoadSourceSet(path, *project)
			if err != nil {
				return err
			}
			program, err = script.CompileSourceSet(sources, host, script.CompileOptions{Entry: route.entry})
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			programs[key] = program
		}
		compiled = append(compiled, script.HTTPRoute{Pattern: route.pattern, Program: program})
	}
	handler, err := script.NewHTTPMux(compiled, script.HTTPOptions{
		Timeout: *timeout, MaxBodyBytes: *bodyLimit, MaxResponseBytes: *responseLimit, MaxConcurrentRequests: *concurrent,
		Run: script.Options{Parallelism: *parallel, MaxSteps: *steps, MaxElements: *elements},
		OnError: func(r *http.Request, report script.Report, err error) {
			fmt.Fprintf(os.Stderr, "HTTP %s %q: %v\n", r.Method, r.URL.Path, err)
		},
	})
	if err != nil {
		return err
	}
	writeTimeout := *timeout
	if writeTimeout == 0 {
		writeTimeout = 30 * time.Second
	}
	if writeTimeout > time.Duration(1<<63-1)-5*time.Second {
		return fmt.Errorf("HTTP timeout is too large")
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{
		Handler: handler, DisableGeneralOptionsHandler: true, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: writeTimeout + 5*time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 64 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	fmt.Fprintf(os.Stderr, "Serving %d route(s) at http://%s\n", len(compiled), listener.Addr())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return server.Close()
		}
		return nil
	}
}

type httpSourceRoute struct{ pattern, source, entry string }

func readHTTPRoutes(path string) ([]httpSourceRoute, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data, err := script.DecodeData(raw)
	if err != nil {
		return nil, fmt.Errorf("routes manifest: %w", err)
	}
	entries, ok := data.Elements()
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("routes manifest must be a nonempty JSON array")
	}
	routes := []httpSourceRoute{}
	for _, entry := range entries {
		fields, ok := entry.Fields()
		pattern, patternOK := fields["pattern"].Text()
		source, sourceOK := fields["source"].Text()
		if !ok || (len(fields) != 2 && len(fields) != 3) || !patternOK || !sourceOK || pattern == "" || source == "" {
			return nil, fmt.Errorf("each route must contain exactly nonempty pattern and source strings")
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(filepath.Dir(path), source)
		}
		selected := ""
		if len(fields) == 3 {
			var entryOK bool
			selected, entryOK = fields["entry"].Text()
			if !entryOK || selected == "" {
				return nil, fmt.Errorf("route entry must be a nonempty string")
			}
		}
		routes = append(routes, httpSourceRoute{pattern: pattern, source: source, entry: selected})
	}
	return routes, nil
}
