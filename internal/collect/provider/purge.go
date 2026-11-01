package provider

import "github.com/NaejEL/opnview/internal/store/plugintables"

// AddPurgeHook registers what one plugin does at the retention purge (ADR 0021, step 6;
// internal/store/plugintables, purgehook.go). The hooks run in the order they were added, which is the
// plugins' load order.
func (s *Set) AddPurgeHook(hook plugintables.PurgeHook) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.purgeHooks = append(s.purgeHooks, hook)
}

// PurgeHooks returns every plugin's purge hook, in load order.
func (s *Set) PurgeHooks() []plugintables.PurgeHook {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return append([]plugintables.PurgeHook(nil), s.purgeHooks...)
}
