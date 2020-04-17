package build

type DefaultPhaseFactory struct {
	lifecycle *Lifecycle
}

func NewDefaultPhaseFactory(lifecycle *Lifecycle) *DefaultPhaseFactory {
	return &DefaultPhaseFactory{lifecycle: lifecycle}
}

func (d *DefaultPhaseFactory) New(provider *PhaseConfigProvider) RunnerCleaner {
	return &Phase{
		ctrConf:    provider.ContainerConfig(),
		hostConf:   provider.HostConfig(),
		name:       provider.Name(),
		docker:     d.lifecycle.docker,
		logger:     d.lifecycle.logger,
		uid:        d.lifecycle.builder.UID(),
		gid:        d.lifecycle.builder.GID(),
		appPath:    d.lifecycle.appPath,
		mountPaths: d.lifecycle.mountPaths,
		appOnce:    d.lifecycle.appOnce,
		fileFilter: d.lifecycle.fileFilter,
		os:         provider.os,
	}
}
