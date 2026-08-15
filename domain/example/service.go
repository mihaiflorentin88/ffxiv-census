package example
import "github.com/mihaiflorentin88/ffxiv-census/port/contract"

// Service is a placeholder domain service. Replace it with your own bounded contexts.
type Service struct {
	repo contract.ExampleRepository
}

func NewService(repo contract.ExampleRepository) *Service {
	return &Service{
		repo: repo,
	}
}

func (s *Service) Ping() string {
	return "pong"
}
