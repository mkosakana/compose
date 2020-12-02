// +build local

/*
   Copyright 2020 Docker Compose CLI authors

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package local

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/docker/docker/errdefs"

	"github.com/compose-spec/compose-go/types"
	"github.com/docker/buildx/build"
	"github.com/docker/buildx/driver"
	_ "github.com/docker/buildx/driver/docker" // required to get default driver registered
	"github.com/docker/buildx/util/progress"
)

func (s *composeService) ensureImagesExists(ctx context.Context, project *types.Project) error {
	opts := map[string]build.Options{}
	for _, service := range project.Services {
		if service.Image == "" && service.Build == nil {
			return fmt.Errorf("invalid service %q. Must specify either image or build", service.Name)
		}

		// TODO build vs pull should be controlled by pull policy, see https://github.com/compose-spec/compose-spec/issues/26
		if service.Image != "" {
			needPull, err := s.needPull(ctx, service)
			if err != nil {
				return err
			}
			if !needPull {
				continue
			}
		}
		if service.Build != nil {
			imageName := service.Image
			if imageName == "" {
				imageName = project.Name + "_" + service.Name
			}
			opts[imageName] = s.toBuildOptions(service, project.WorkingDir)
			continue
		}

		// Buildx has no command to "just pull", see
		// so we bake a temporary dockerfile that will just pull and export pulled image
		opts[service.Name] = build.Options{
			Inputs: build.Inputs{
				ContextPath:    ".",
				DockerfilePath: "-",
				InStream:       strings.NewReader("FROM " + service.Image),
			},
			Tags: []string{service.Image},
			Pull: true,
		}

	}

	return s.build(ctx, project, opts)
}

func (s *composeService) needPull(ctx context.Context, service types.ServiceConfig) (bool, error) {
	_, _, err := s.apiClient.ImageInspectWithRaw(ctx, service.Image)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (s *composeService) build(ctx context.Context, project *types.Project, opts map[string]build.Options) error {
	if len(opts) == 0 {
		return nil
	}
	const drivername = "default"
	d, err := driver.GetDriver(ctx, drivername, nil, s.apiClient, nil, nil, "", nil, project.WorkingDir)
	if err != nil {
		return err
	}
	driverInfo := []build.DriverInfo{
		{
			Name:   "default",
			Driver: d,
		},
	}
	// We rely on buildx "docker" builder integrated in docker engine, so don't need a DockerAPI here
	w := progress.NewPrinter(ctx, os.Stdout, "auto")
	_, err = build.Build(ctx, driverInfo, opts, nil, nil, w)
	return err
}

func (s *composeService) toBuildOptions(service types.ServiceConfig, contextPath string) build.Options {
	var tags []string
	if service.Image != "" {
		tags = append(tags, service.Image)
	}

	if service.Build.Dockerfile == "" {
		service.Build.Dockerfile = "Dockerfile"
	}
	var buildArgs map[string]string

	return build.Options{
		Inputs: build.Inputs{
			ContextPath:    path.Join(contextPath, service.Build.Context),
			DockerfilePath: path.Join(contextPath, service.Build.Context, service.Build.Dockerfile),
		},
		BuildArgs: flatten(mergeArgs(service.Build.Args, buildArgs)),
		Tags:      tags,
	}
}

func flatten(in types.MappingWithEquals) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string)
	for k, v := range in {
		if v == nil {
			continue
		}
		out[k] = *v
	}
	return out
}

func mergeArgs(src types.MappingWithEquals, values map[string]string) types.MappingWithEquals {
	for key := range src {
		if val, ok := values[key]; ok {
			if val == "" {
				src[key] = nil
			} else {
				src[key] = &val
			}
		}
	}
	return src
}
