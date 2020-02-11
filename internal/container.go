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
