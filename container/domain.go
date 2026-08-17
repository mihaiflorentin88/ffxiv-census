package container

import (
	"context"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
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
	// Seed the milestone registry (idempotent) so achievement processing never
	// runs against an empty registry.
	if err := svc.SyncMilestones(context.Background()); err != nil {
		logging.Error("container.census", fmt.Sprintf("failed to sync milestones: %v", err))
	}
	s.domain.censusService = svc
	return svc
}

// Handlers returns a registry of ingest handlers, each wired to its
// dependencies. Handlers are stateless, so a fresh registry per call is fine.
func (s *ServiceContainer) Handlers() *handler.Registry {
	reg := handler.NewRegistry()
	reg.Register(handler.EventIDSweep, handler.NewIDSweep(s.LodestoneClient(), s.CensusService()))
	reg.Register(handler.EventAchievementCensus, handler.NewAchievementCensus(s.LodestoneClient(), s.CensusService()))
	return reg
}
