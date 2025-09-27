package plugin_manager

import (
	"sync/atomic"

	"github.com/langgenius/dify-plugin-daemon/pkg/entities/plugin_entities"
)

type runtimeTrafficState struct {
	sessions atomic.Int64
	draining atomic.Bool
}

type registerRuntimeOptions struct {
	blueGreen bool
}

type RuntimeTrafficReport struct {
	Identity string
	PluginID string
	Sessions int64
	Draining bool
}

func (p *PluginManager) ensureRuntimeState(identity string) *runtimeTrafficState {
	stateAny, _ := p.runtimeSessions.LoadOrStore(identity, &runtimeTrafficState{})
	state, ok := stateAny.(*runtimeTrafficState)
	if !ok {
		// this should never happen, but protect against panic if the map was polluted
		newState := &runtimeTrafficState{}
		p.runtimeSessions.Store(identity, newState)
		return newState
	}
	return state
}

func (p *PluginManager) cleanupRuntime(identity string) {
	p.runtimeSessions.Delete(identity)
	p.runtimePluginIDs.Delete(identity)
}

func (p *PluginManager) loadRuntimeState(identity string) (*runtimeTrafficState, bool) {
	stateAny, ok := p.runtimeSessions.Load(identity)
	if !ok {
		return nil, false
	}
	state, ok := stateAny.(*runtimeTrafficState)
	if !ok {
		return nil, false
	}
	return state, true
}

func (p *PluginManager) stopRuntime(identity string) {
	if runtime, ok := p.m.Load(identity); ok {
		runtime.Stop()
	}
	p.cleanupRuntime(identity)
}

func (p *PluginManager) markRuntimeDraining(identity string) {
	stateAny, ok := p.runtimeSessions.Load(identity)
	if ok {
		if state, ok := stateAny.(*runtimeTrafficState); ok {
			state.draining.Store(true)
			if state.sessions.Load() == 0 {
				if runtime, ok := p.m.Load(identity); ok {
					runtime.Stop()
				}
				p.cleanupRuntime(identity)
			}
			return
		}
	}

	if runtime, ok := p.m.Load(identity); ok {
		runtime.Stop()
	}
	p.cleanupRuntime(identity)
}

func (p *PluginManager) registerRuntime(
	identity plugin_entities.PluginUniqueIdentifier,
	options registerRuntimeOptions,
) {
	identityStr := identity.String()
	pluginID := identity.PluginID()
	p.runtimePluginIDs.Store(identityStr, pluginID)

	state := p.ensureRuntimeState(identityStr)
	state.draining.Store(false)

	if options.blueGreen {
		p.runtimePluginIDs.Range(func(key, value any) bool {
			identityKey := key.(string)
			if identityKey == identityStr {
				return true
			}
			if value.(string) == pluginID {
				p.markRuntimeDraining(identityKey)
			}
			return true
		})
		return
	}

	p.runtimePluginIDs.Range(func(key, value any) bool {
		identityKey := key.(string)
		if identityKey == identityStr {
			return true
		}
		if value.(string) == pluginID {
			p.stopRuntime(identityKey)
		}
		return true
	})
}

func (p *PluginManager) AcquireRuntime(identity plugin_entities.PluginUniqueIdentifier) (plugin_entities.PluginLifetime, func(), error) {
	runtime, err := p.Get(identity)
	if err != nil {
		return nil, nil, err
	}

	state := p.ensureRuntimeState(identity.String())
	state.sessions.Add(1)

	var released atomic.Bool
	release := func() {
		if !released.CompareAndSwap(false, true) {
			return
		}

		remaining := state.sessions.Add(-1)
		if remaining < 0 {
			state.sessions.Store(0)
			remaining = 0
		}

		if state.draining.Load() && remaining == 0 {
			if runtime, ok := p.m.Load(identity.String()); ok {
				runtime.Stop()
			}
			p.cleanupRuntime(identity.String())
		}
	}

	return runtime, release, nil
}

func (p *PluginManager) CollectRuntimeTraffic(filterPluginID string) []RuntimeTrafficReport {
	reports := make([]RuntimeTrafficReport, 0)

	p.runtimePluginIDs.Range(func(key, value any) bool {
		identity := key.(string)
		pluginID := value.(string)
		if filterPluginID != "" && filterPluginID != pluginID {
			return true
		}

		var (
			sessions int64
			draining bool
		)

		if state, ok := p.loadRuntimeState(identity); ok {
			sessions = state.sessions.Load()
			draining = state.draining.Load()
		}

		reports = append(reports, RuntimeTrafficReport{
			Identity: identity,
			PluginID: pluginID,
			Sessions: sessions,
			Draining: draining,
		})
		return true
	})

	return reports
}
