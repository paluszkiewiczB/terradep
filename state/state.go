package state

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"go.interactor.dev/terradep"
)

// ByBackendStater stores instances of [terradep.Stater] assigned to parsing specific type of backend
type ByBackendStater struct {
	staters map[string]terradep.Stater
}

// NewByTypeStater returns new configured instance of [ByBackendStater]
func NewByTypeStater(staters map[string]terradep.Stater) *ByBackendStater {
	return &ByBackendStater{
		staters: staters,
	}
}

// BackendState implements [terradep.Stater]
func (s *ByBackendStater) BackendState(backend string, body hcl.Body) (terradep.State, error) {
	next, ok := s.staters[backend]
	if !ok {
		return nil, fmt.Errorf("supported backends: %v, got: %q", s.supportedBackends(), backend)
	}

	return next.BackendState(backend, body)
}

// RemoteState implements [terradep.Stater]
func (s *ByBackendStater) RemoteState(backend string, stateCfg map[string]cty.Value) (terradep.State, error) {
	next, ok := s.staters[backend]
	if !ok {
		return nil, fmt.Errorf("supported backends: %v, got: %q", s.supportedBackends(), backend)
	}

	return next.RemoteState(backend, stateCfg)
}

func (s *ByBackendStater) supportedBackends() []string {
	backends := make([]string, 0, len(s.staters))
	for backend := range s.staters {
		backends = append(backends, backend)
	}
	return backends
}
