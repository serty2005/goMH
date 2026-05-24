package registry

import (
	"goMH/config"
	"goMH/core"
	"goMH/modules/distro"
	fiscaldrivers "goMH/modules/fiscal-drivers"
	"goMH/modules/frpc"
	"goMH/modules/regime"
	"goMH/modules/remoteaccess"
	"goMH/modules/serviceutils"
	"goMH/modules/utm"
	"goMH/modules/vcomcaster"
)

type Factory func() core.QueueModule

type Registry struct {
	factories map[string]Factory
}

func New(factories ...Factory) *Registry {
	registry := &Registry{
		factories: make(map[string]Factory, len(factories)),
	}
	for _, factory := range factories {
		registry.Register(factory)
	}
	return registry
}

func NewDefault() *Registry {
	return New(
		func() core.QueueModule { return &vcomcaster.Module{} },
		func() core.QueueModule { return &distro.Module{} },
		func() core.QueueModule { return &frpc.Module{} },
		func() core.QueueModule { return &regime.Module{} },
		func() core.QueueModule { return &remoteaccess.Module{} },
		func() core.QueueModule { return &serviceutils.Module{} },
		func() core.QueueModule { return &fiscaldrivers.Module{} },
		func() core.QueueModule { return &utm.Module{} },
	)
}

func (r *Registry) Register(factory Factory) {
	if factory == nil {
		return
	}
	module := factory()
	if module == nil {
		return
	}
	r.factories[module.ID()] = factory
}

func (r *Registry) Get(id string) (core.QueueModule, bool) {
	factory, ok := r.factories[id]
	if !ok {
		return nil, false
	}
	module := factory()
	if module == nil {
		return nil, false
	}
	return module, true
}

func (r *Registry) Enabled(defs []config.ModuleDef) []core.QueueModule {
	modules := make([]core.QueueModule, 0, len(defs))
	for _, def := range defs {
		module, ok := r.Get(def.ID)
		if !ok {
			continue
		}
		modules = append(modules, module)
	}
	return modules
}
