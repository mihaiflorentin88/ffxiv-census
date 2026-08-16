package container

import "github.com/mihaiflorentin88/ffxiv-census/domain/census"

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
