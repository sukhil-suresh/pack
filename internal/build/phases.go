package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/buildpacks/lifecycle/auth"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/pkg/errors"

	"github.com/buildpacks/pack/internal/api"
	"github.com/buildpacks/pack/internal/archive"
	"github.com/buildpacks/pack/internal/style"
)

const defaultProcessPlatformAPI = "0.3"

type RunnerCleaner interface {
	Run(ctx context.Context) error
	Cleanup() error
}

type PhaseFactory interface {
	New(provider *PhaseConfigProvider) RunnerCleaner
}

// TODO: Test this
func (l *Lifecycle) prepareAppVolume(ctx context.Context) error {
	tarExtractPath := "/"
	entrypoint := ""
	if l.os == "windows" {
		tarExtractPath = "/windows"
		entrypoint = fmt.Sprintf(`xcopy c:%s\%s %s /E /H /Y /C /B &&`, style.Symbol(l.mountPaths.appDir()), strings.ReplaceAll(tarExtractPath, "/", `\`), l.mountPaths.appDirName(), l.mountPaths.appDir())
	}

	ctr, err := l.docker.ContainerCreate(ctx,
		&container.Config{
			Entrypoint: []string{entrypoint},
		},
		&container.HostConfig{
			Binds: []string{fmt.Sprintf("%s:%s", l.AppVolume, l.mountPaths.appDir())},
		},
		nil, "",
	)
	if err != nil {
		return errors.Wrapf(err, "creating prep container")
	}
	defer l.docker.ContainerRemove(context.Background(), ctr.ID, types.ContainerRemoveOptions{Force: true})

	if l.os == "windows" {
		// run container
	}
	return nil
}

func (l *Lifecycle) createAppReader() (io.ReadCloser, error) {
	fi, err := os.Stat(l.appPath)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		var mode int64 = -1
		if runtime.GOOS == "windows" {
			mode = 0777
		}

		return archive.ReadDirAsTar(l.appPath, "/"+l.mountPaths.appDirName(), l.builder.UID(), l.builder.GID(), mode, false, l.fileFilter), nil
	}

	return archive.ReadZipAsTar(l.appPath, "/"+l.mountPaths.appDirName(), l.builder.UID(), l.builder.GID(), -1, false, l.fileFilter), nil
}

func (l *Lifecycle) copyAppToContainer(ctx context.Context, container, path string) error {
	appReader, err := l.createAppReader()
	if err != nil {
		return errors.Wrapf(err, "create tar archive from '%s'", l.appPath)
	}
	defer appReader.Close()

	err = l.docker.CopyToContainer(ctx, container, path, appReader, types.CopyToContainerOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to copy app directory")
	}

	return nil
}

func (l *Lifecycle) Detect(ctx context.Context, networkMode string, volumes []string, phaseFactory PhaseFactory) error {
	configProvider := NewPhaseConfigProvider(
		"detector",
		l,
		WithArgs(
			l.withLogLevel(
				"-app", l.mountPaths.appDir(),
				"-platform", l.mountPaths.platformDir(),
			)...,
		),
		WithNetwork(networkMode),
		WithBinds(volumes...),
	)

	detect := phaseFactory.New(configProvider)
	defer detect.Cleanup()
	return detect.Run(ctx)
}

func (l *Lifecycle) Restore(ctx context.Context, cacheName string, phaseFactory PhaseFactory) error {
	configProvider := NewPhaseConfigProvider(
		"restorer",
		l,
		WithDaemonAccess(),
		WithArgs(
			l.withLogLevel(
				"-cache-dir", l.mountPaths.cacheDir(),
				"-layers", l.mountPaths.layersDir(),
			)...,
		),
		WithBinds(fmt.Sprintf("%s:%s", cacheName, l.mountPaths.cacheDir())),
	)

	restore := phaseFactory.New(configProvider)
	defer restore.Cleanup()
	return restore.Run(ctx)
}

func (l *Lifecycle) Analyze(ctx context.Context, repoName, cacheName string, publish, clearCache bool, phaseFactory PhaseFactory) error {
	analyze, err := l.newAnalyze(repoName, cacheName, publish, clearCache, phaseFactory)
	if err != nil {
		return err
	}
	defer analyze.Cleanup()
	return analyze.Run(ctx)
}

func (l *Lifecycle) newAnalyze(repoName, cacheName string, publish, clearCache bool, phaseFactory PhaseFactory) (RunnerCleaner, error) {
	args := []string{
		"-layers", l.mountPaths.layersDir(),
		repoName,
	}
	if clearCache {
		args = prependArg("-skip-layers", args)
	} else {
		args = append([]string{"-cache-dir", l.mountPaths.cacheDir()}, args...)
	}

	if publish {
		authConfig, err := auth.BuildEnvVar(authn.DefaultKeychain, repoName)
		if err != nil {
			return nil, err
		}

		configProvider := NewPhaseConfigProvider(
			"analyzer",
			l,
			WithRegistryAccess(authConfig),
			WithRoot(),
			WithArgs(args...),
			WithBinds(fmt.Sprintf("%s:%s", cacheName, l.mountPaths.cacheDir())),
		)

		return phaseFactory.New(configProvider), nil
	}

	configProvider := NewPhaseConfigProvider(
		"analyzer",
		l,
		WithDaemonAccess(),
		WithArgs(
			l.withLogLevel(
				prependArg(
					"-daemon",
					args,
				)...,
			)...,
		),
		WithBinds(fmt.Sprintf("%s:%s", cacheName, l.mountPaths.cacheDir())),
	)

	return phaseFactory.New(configProvider), nil
}

func prependArg(arg string, args []string) []string {
	return append([]string{arg}, args...)
}

func (l *Lifecycle) Build(ctx context.Context, networkMode string, volumes []string, phaseFactory PhaseFactory) error {
	configProvider := NewPhaseConfigProvider(
		"builder",
		l,
		WithArgs(
			"-layers", l.mountPaths.layersDir(),
			"-app", l.mountPaths.appDir(),
			"-platform", l.mountPaths.platformDir(),
		),
		WithNetwork(networkMode),
		WithBinds(volumes...),
	)

	build := phaseFactory.New(configProvider)
	defer build.Cleanup()
	return build.Run(ctx)
}

func (l *Lifecycle) Export(ctx context.Context, repoName string, runImage string, publish bool, launchCacheName, cacheName string, phaseFactory PhaseFactory) error {
	export, err := l.newExport(repoName, runImage, publish, launchCacheName, cacheName, phaseFactory)
	if err != nil {
		return err
	}
	defer export.Cleanup()
	return export.Run(ctx)
}

func (l *Lifecycle) newExport(repoName, runImage string, publish bool, launchCacheName, cacheName string, phaseFactory PhaseFactory) (RunnerCleaner, error) {
	args := l.exportImageArgs(runImage)
	args = append(args, []string{
		"-cache-dir", l.mountPaths.cacheDir(),
		"-layers", l.mountPaths.layersDir(),
		"-app", l.mountPaths.appDir(),
		repoName,
	}...)

	binds := []string{fmt.Sprintf("%s:%s", cacheName, l.mountPaths.cacheDir())}

	if publish {
		authConfig, err := auth.BuildEnvVar(authn.DefaultKeychain, repoName, runImage)
		if err != nil {
			return nil, err
		}

		configProvider := NewPhaseConfigProvider(
			"exporter",
			l,
			WithRegistryAccess(authConfig),
			WithArgs(
				l.withLogLevel(args...)...,
			),
			WithRoot(),
			WithBinds(binds...),
		)

		return phaseFactory.New(configProvider), nil
	}

	args = append([]string{"-daemon", "-launch-cache", l.mountPaths.launchCacheDir()}, args...)
	binds = append(binds, fmt.Sprintf("%s:%s", launchCacheName, l.mountPaths.launchCacheDir()))

	if l.DefaultProcessType != "" {
		supportsDefaultProcess := api.MustParse(l.platformAPIVersion).SupportsVersion(api.MustParse(defaultProcessPlatformAPI))
		if supportsDefaultProcess {
			args = append([]string{"-process-type", l.DefaultProcessType}, args...)
		} else {
			l.logger.Warn("You specified a default process type but that is not supported by this version of the lifecycle")
		}
	}

	configProvider := NewPhaseConfigProvider(
		"exporter",
		l,
		WithDaemonAccess(),
		WithArgs(
			l.withLogLevel(args...)...,
		),
		WithBinds(binds...),
	)

	return phaseFactory.New(configProvider), nil
}

func (l *Lifecycle) withLogLevel(args ...string) []string {
	version := semver.MustParse(l.version)
	if semver.MustParse("0.4.0").LessThan(version) {
		if l.logger.IsVerbose() {
			return append([]string{"-log-level", "debug"}, args...)
		}
	}
	return args
}

func (l *Lifecycle) exportImageArgs(runImage string) []string {
	platformAPIVersion := semver.MustParse(l.platformAPIVersion)
	if semver.MustParse("0.2").LessThan(platformAPIVersion) {
		return []string{"-run-image", runImage}
	}
	return []string{"-image", runImage}
}
