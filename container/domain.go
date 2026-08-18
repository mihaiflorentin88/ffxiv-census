package container

import (
	"context"
	"fmt"
	"time"

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
	achievements := s.AchievementRepository()
	if achievements == nil {
		logging.Warn("container.census", "sqlite driver unavailable, census service disabled")
		return nil
	}
	svc := census.NewService(
		s.CharacterRepository(),
		s.FreeCompanyRepository(),
		achievements,
		s.CensusRunRepository(),
	)
	// Honor the configured activity window and expansions ([census] in config.toml).
	if c := s.Config().Census; c != nil {
		if c.ActivityWindowDays > 0 {
			svc.SetActivityWindow(time.Duration(c.ActivityWindowDays) * 24 * time.Hour)
		}
		var expansions []census.ExpansionConfig
		for _, exp := range c.Expansions {
			expansions = append(expansions, census.ExpansionConfig{
				Name:          exp.Name,
				Version:       exp.Version,
				FinalQuest:    exp.FinalQuest,
				Icon:          exp.Icon,
				LevelCap:      exp.LevelCap,
				AchievementID: exp.AchievementID,
			})
		}
		svc.SetConfig(c.MaxLevel, expansions)
	}
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
// When the census service is unavailable (no SQLite), an empty registry is
// returned and the worker reports "no handler registered" rather than panicking.
func (s *ServiceContainer) Handlers() *handler.Registry {
	reg := handler.NewRegistry()
	svc := s.CensusService()
	if svc == nil {
		return reg
	}
	reg.Register(handler.EventIDSweep, handler.NewIDSweep(s.LodestoneClient(), s.TomestoneClient(), svc, s.Logger()))
	reg.Register(handler.EventAchievementCensus, handler.NewAchievementCensus(s.LodestoneClient(), svc, s.Logger()))
	reg.Register(handler.EventCharacterCensus, handler.NewCharacterCensus(s.LodestoneClient(), svc, s.Logger()))
	reg.Register(handler.EventFreeCompanyCensus, handler.NewFreeCompanyCensus(s.LodestoneClient(), svc, s.Logger()))
	return reg
}
