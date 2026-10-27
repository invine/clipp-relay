package auth

import (
	"context"
	"errors"
)

// ErrRetainedPepperUnavailable allows startup to keep existing accounts online
// while registration remains closed until the current-week key is restored.
var ErrRetainedPepperUnavailable = errors.New("retained usage key unavailable")

// ValidatePepperCoverage protects Portal Sessions and current-week retained
// usage. OAuth credentials use the independent native signing key.
func (s *Server) ValidatePepperCoverage(ctx context.Context) error {
	coverage, err := s.maintenance.PepperCoverage(ctx)
	if err != nil {
		return errors.New("pepper coverage unavailable")
	}
	for _, version := range coverage.PortalSessions {
		if len(s.Peppers[version]) == 0 {
			return errors.New("pepper version still protects credentials")
		}
	}
	for _, version := range coverage.RetainedUsage {
		if len(s.Peppers[version]) == 0 {
			return ErrRetainedPepperUnavailable
		}
	}
	return nil
}
