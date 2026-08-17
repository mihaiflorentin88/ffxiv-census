package container

import (
	"fmt"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/config"
)

var Load *ServiceContainer

type ServiceContainer struct {
	mu             sync.Mutex
	infrastructure *InfrastructureContainer
	domain         *DomainContainer
	config         *config.Config
}

func NewServiceContainer() *ServiceContainer {
	return &ServiceContainer{
		infrastructure: &InfrastructureContainer{},
		domain:         &DomainContainer{},
	}
}

func (s *ServiceContainer) Config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config != nil {
		return s.config
	}
	cfg, err := config.NewConfig()
	if err != nil {
		panic(fmt.Sprintf("load config: %v", err))
	}
	s.config = cfg
	return cfg
}

func (s *ServiceContainer) configUnlocked() *config.Config {
	if s.config != nil {
		return s.config
	}
	cfg, err := config.NewConfig()
	if err != nil {
		panic(fmt.Sprintf("load config: %v", err))
	}
	s.config = cfg
	return cfg
}
