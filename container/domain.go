package container

import (
	"github.com/mihaiflorentin88/ffxiv-census/domain/example"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type DomainContainer struct {
	exampleService contract.ExampleService
}

func (s *ServiceContainer) ExampleService() contract.ExampleService {
	if s.domain.exampleService != nil {
		return s.domain.exampleService
	}
	svc := example.NewService(s.ExampleRepository())
	s.domain.exampleService = svc
	return svc
}
