package container

import (
	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
)

// DomainContainer wires domain services.
type DomainContainer struct {
	censusService *census.Service
}

func (s *ServiceContainer) CensusService() *census.Service {
	if s.domain.censusService != nil {
		return s.domain.censusService
	}
	svc := census.NewService(
		s.CharacterRepository(),
		s.FreeCompanyRepository(),
		s.AchievementRepository(),
		s.CensusRunRepository(),
	)
	s.domain.censusService = svc
	return svc
}

// Handlers returns a registry of ingest handlers, each wired to its
// dependencies. Handlers are stateless, so a fresh registry per call is fine.
func (s *ServiceContainer) Handlers() *handler.Registry {
	reg := handler.NewRegistry()
	reg.Register(handler.EventIDSweep, handler.NewIDSweep(s.LodestoneClient(), s.CensusService()))
	return reg
}
