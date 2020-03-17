/*
   Copyright 2020 The Compose Specification Authors.

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

package internal

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

func CollectContainers(cli *client.Client, project string) (map[string][]types.Container, error) {
	containerList, err := cli.ContainerList(context.Background(), types.ContainerListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", LabelProject+"="+project)),
	})
	if err != nil {
		return nil, err
	}
	containers := map[string][]types.Container{}
	for _, c := range containerList {
		service := c.Labels[LabelService]
		l, ok := containers[service]
		if !ok {
			l = []types.Container{c}
		} else {
			l = append(l, c)
		}
		containers[service] = l
	}
	return containers, nil
}

func RemoveContainers(cli *client.Client, containers []types.Container) error {
	ctx := context.Background()
	for _, c := range containers {
		if serviceName, ok := c.Labels[LabelService]; ok {
			fmt.Printf("Stopping containers for service %s ... ", serviceName)
		}
		err := cli.ContainerStop(ctx, c.ID, nil)
		if err != nil {
			return err
		}
		err = cli.ContainerRemove(ctx, c.ID, types.ContainerRemoveOptions{})
		if err != nil {
			return err
		}
		fmt.Println(c.ID)
	}
	return nil
}
