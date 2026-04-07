package router

import (
	"fmt"
	"sort"
	"strings"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
)

type Route struct {
	Name            string
	Priority        int
	InboundProtocol pipeline.Protocol
	PathPrefix      string
	EndpointKinds   map[pipeline.EndpointKind]bool
	BackendName     string
}

type ResolvedRoute struct {
	Route        *Route
	EndpointKind pipeline.EndpointKind
}

type Router struct {
	routes []*Route
}

func New(cfg config.RoutingConfig, backends []config.BackendConfig) (*Router, error) {
	backendSet := make(map[string]bool, len(backends))
	for _, b := range backends {
		backendSet[b.Name] = true
	}

	routes := make([]*Route, 0, len(cfg.Routes))
	for _, rc := range cfg.Routes {
		if !backendSet[rc.Backend] {
			return nil, fmt.Errorf("route %q references unknown backend %q", rc.Name, rc.Backend)
		}
		kinds := make(map[pipeline.EndpointKind]bool, len(rc.EndpointKinds))
		for _, k := range rc.EndpointKinds {
			kinds[pipeline.EndpointKind(k)] = true
		}
		routes = append(routes, &Route{
			Name:            rc.Name,
			Priority:        rc.Priority,
			InboundProtocol: pipeline.Protocol(rc.InboundProtocol),
			PathPrefix:      rc.PathPrefix,
			EndpointKinds:   kinds,
			BackendName:     rc.Backend,
		})
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Priority != routes[j].Priority {
			return routes[i].Priority > routes[j].Priority
		}
		return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
	})

	if err := validateNoAmbiguity(routes); err != nil {
		return nil, err
	}

	return &Router{routes: routes}, nil
}

func validateNoAmbiguity(routes []*Route) error {
	for i := 0; i < len(routes); i++ {
		for j := i + 1; j < len(routes); j++ {
			a, b := routes[i], routes[j]
			if a.Priority != b.Priority {
				continue
			}
			if len(a.PathPrefix) != len(b.PathPrefix) {
				continue
			}
			if a.InboundProtocol != b.InboundProtocol {
				continue
			}
			if !prefixesOverlap(a.PathPrefix, b.PathPrefix) {
				continue
			}
			if !kindsOverlap(a.EndpointKinds, b.EndpointKinds) {
				continue
			}
			return fmt.Errorf(
				"ambiguous routes: %q and %q have same priority (%d), prefix length, protocol, and overlapping endpoint kinds",
				a.Name, b.Name, a.Priority,
			)
		}
	}
	return nil
}

func prefixesOverlap(a, b string) bool {
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func kindsOverlap(a, b map[pipeline.EndpointKind]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

func (r *Router) Resolve(path string) (*ResolvedRoute, error) {
	for _, route := range r.routes {
		if !strings.HasPrefix(path, route.PathPrefix) {
			continue
		}

		kind := inferEndpointKind(path, route)
		if kind == "" && len(route.EndpointKinds) == 1 {
			for k := range route.EndpointKinds {
				kind = k
			}
		}

		if kind != "" && !route.EndpointKinds[kind] {
			continue
		}

		return &ResolvedRoute{Route: route, EndpointKind: kind}, nil
	}

	return nil, &pipeline.PipelineError{
		StatusCode: 404,
		Message:    fmt.Sprintf("no route matched for %s", path),
	}
}

func inferEndpointKind(path string, route *Route) pipeline.EndpointKind {
	suffix := strings.TrimPrefix(path, route.PathPrefix)
	if suffix == "" || suffix == "/" {
		if len(route.EndpointKinds) == 1 {
			for k := range route.EndpointKinds {
				return k
			}
		}
	}

	switch route.InboundProtocol {
	case pipeline.ProtocolOpenAI:
		return detectOpenAIEndpoint(path)
	case pipeline.ProtocolAnthropic:
		return detectAnthropicEndpoint(path)
	}
	return ""
}

func detectOpenAIEndpoint(path string) pipeline.EndpointKind {
	if strings.Contains(path, "/chat/completions") {
		return pipeline.EndpointChat
	}
	if strings.HasSuffix(path, "/completions") {
		return pipeline.EndpointCompletion
	}
	if strings.Contains(path, "/embeddings") {
		return pipeline.EndpointEmbedding
	}
	if strings.Contains(path, "/models") {
		return pipeline.EndpointModelList
	}
	return ""
}

func detectAnthropicEndpoint(path string) pipeline.EndpointKind {
	if strings.Contains(path, "/messages") {
		return pipeline.EndpointChat
	}
	return ""
}
