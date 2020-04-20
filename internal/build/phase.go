package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/docker/docker/api/types"
	dcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"

	"github.com/buildpacks/pack/internal/archive"
	"github.com/buildpacks/pack/internal/container"
	"github.com/buildpacks/pack/internal/style"
	"github.com/buildpacks/pack/logging"
)

type Phase struct {
	name       string
	logger     logging.Logger
	docker     client.CommonAPIClient
	ctrConf    *dcontainer.Config
	hostConf   *dcontainer.HostConfig
	ctr        dcontainer.ContainerCreateCreatedBody
	uid, gid   int
	fileFilter func(string) bool
}

func (p *Phase) Run(ctx context.Context) error {
	var err error

	tarExtractPath := "/"
	if p.os == "windows" && p.name == "detector" {
		// NOTE: Because Windows containers apparently do not allow populating a volume via 'docker copy', we have to
		// set the copy destination to a temporary directory and populate the app volume from inside the container
		// (by inserting a copy just before the usual entrypoint). This causes some overhead in the extra copy, but only
		// occurs once per build.
		tarExtractPath = "/windows"
		p.ctrConf.Entrypoint = append(
			[]string{
				"cmd",
				"/c",
				fmt.Sprintf(`echo Copying app directory to %s && xcopy c:%s\%s %s /E /H /Y /C /B &&`, style.Symbol(p.mountPaths.appDir()), strings.ReplaceAll(tarExtractPath, "/", `\`), p.mountPaths.appDirName(), p.mountPaths.appDir()),
			},
			p.ctrConf.Entrypoint...
		)
	}

	p.ctr, err = p.docker.ContainerCreate(ctx, p.ctrConf, p.hostConf, nil, "")
	if err != nil {
		return errors.Wrapf(err, "failed to create '%s' container", p.name)
	}

	p.appOnce.Do(func() {
		var (
			appReader io.ReadCloser
			// clientErr error
		)
		appReader, err = p.createAppReader()


		// if windows, create container, copy to container, run internal copy, trash container


	})

	if err != nil {
		return errors.Wrapf(err, "failed to copy files to '%s' container", p.name)
	}

	return container.Run(
		ctx,
		p.docker,
		p.ctr.ID,
		logging.NewPrefixWriter(logging.GetWriterForLevel(p.logger, logging.InfoLevel), p.name),
		logging.NewPrefixWriter(logging.GetWriterForLevel(p.logger, logging.ErrorLevel), p.name),
	)
}

func (p *Phase) Cleanup() error {
	return p.docker.ContainerRemove(context.Background(), p.ctr.ID, types.ContainerRemoveOptions{Force: true})
}
