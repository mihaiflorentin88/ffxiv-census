package container

import (
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/config"
)

var Load *ServiceContainer

type ServiceContainer struct {
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
