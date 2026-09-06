package runtime

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/alligatorcodes/go-serve/internal/config"
	"github.com/alligatorcodes/go-serve/internal/dataplane"
)

// Manager owns the active runtime boundary shared by the control and data planes.
// Activation compiles the candidate before replacing the current runtime.
type Manager struct {
	mu        sync.RWMutex
	config    config.Config
	dataPlane *dataplane.Server
	events    dataplane.EventSink
}

func New(cfg config.Config, authorizer dataplane.Authorizer) (*Manager, error) {
	return NewWithEvents(cfg, authorizer, nil)
}

func NewWithEvents(cfg config.Config, authorizer dataplane.Authorizer, events dataplane.EventSink) (*Manager, error) {
	dataPlane, err := dataplane.NewServerWithEvents(cfg, authorizer, events)
	if err != nil {
		return nil, err
	}
	return &Manager{config: cfg.Clone(), dataPlane: dataPlane, events: events}, nil
}

func (manager *Manager) DataPlane() *dataplane.Server {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.dataPlane
}

func (manager *Manager) Config() config.Config {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.config.Clone()
}

func (manager *Manager) Activate(candidate config.Config) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !reflect.DeepEqual(manager.config.Server, candidate.Server) || !reflect.DeepEqual(manager.config.Auth, candidate.Auth) || !reflect.DeepEqual(manager.config.Cluster, candidate.Cluster) {
		return fmt.Errorf("server listeners, authentication, and cluster settings require restart")
	}
	if err := manager.dataPlane.Reconfigure(candidate); err != nil {
		if manager.events != nil {
			manager.events.RecordActivation()
		}
		return err
	}
	if manager.events != nil {
		manager.events.RecordActivation()
	}
	manager.config = candidate.Clone()
	return nil
}

func (manager *Manager) Close() error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	manager.dataPlane.Close()
	return nil
}
