package web

import (
	"context"

	"github.com/NaejEL/opnview/internal/config"
)

// Configuration is the configuration in force: the live holder's, when the
// service runs one, or the database's. The live holder is what the scheduler and
// the collectors read, so a surface shows the values they are polling at; a
// server built without one, as in a test, reads the rows the same way start-up
// does.
func (s *Server) Configuration(ctx context.Context) (config.Config, error) {
	if s.settings != nil {
		return s.settings.Get(), nil
	}
	return config.Load(ctx, s.store)
}

// ApplyConfiguration loads the configuration again and hands it to whatever is
// running: the live holder the scheduler reads its intervals from, and the
// collector that reads its page sizes. Either may be absent, in a test or before
// anything runs; the rows are stored either way, and the next start reads them.
//
// IT LOADS RATHER THAN PATCHES, the way start-up loads, so a running service and
// a restarted one cannot disagree about what is in force.
func (s *Server) ApplyConfiguration(ctx context.Context) error {
	settings, err := config.Load(ctx, s.store)
	if err != nil {
		return err
	}
	if s.settings != nil {
		s.settings.Set(settings)
	}
	if s.collector != nil {
		s.collector.Configure(settings)
	}
	return nil
}
