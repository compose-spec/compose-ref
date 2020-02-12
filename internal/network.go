package internal

import (
	"context"
	"fmt"

	compose "github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
)

func GetNetworksFromConfig(cli *client.Client, project string, config *compose.Config) (map[string]string, error) {
	networks := map[string]string{}
	for defaultNetworkName, networkConfig := range config.Networks {
		name, id, err := createNetwork(cli, project, defaultNetworkName, networkConfig)
		if err != nil {
			return nil, err
		}
		networks[name] = id
	}
	if _, ok := networks["default"]; !ok {
		name, id, err := createNetwork(cli, project, "default",
			compose.NetworkConfig{Name: fmt.Sprintf("%s-default", project)})
		if err != nil {
			return nil, err
		}
		networks[name] = id
	}
	return networks, nil
}

func createNetwork(cli *client.Client, project string, networkDefaultName string, netConfig compose.NetworkConfig) (string, string, error) {
	name := networkDefaultName
	if netConfig.Name != "" {
		name = netConfig.Name
	}
	createOptions := types.NetworkCreate{
		Driver:     netConfig.Driver,
		Internal:   netConfig.Internal,
		Attachable: netConfig.Attachable,
		Options:    netConfig.DriverOpts,
		Labels:     netConfig.Labels,
	}
	if createOptions.Driver == "" {
		createOptions.Driver = "bridge" //default driver
	}
	if createOptions.Labels == nil {
		createOptions.Labels = map[string]string{}
	}
	createOptions.Labels[LabelProject] = project
	createOptions.Labels[LabelNetwork] = name

	if netConfig.External.External {
		_, err := cli.NetworkInspect(context.Background(), name, types.NetworkInspectOptions{})
		fmt.Printf("Network %s declared as external. No new network will be created.\n", name)
		if errdefs.IsNotFound(err) {
			return "", "", fmt.Errorf("network %s declared as external, but could not be found. "+
				"Please create the network manually using `docker network create %s` and try again",
				name, name)
		}
	}

	var networkID string
	resource, err := cli.NetworkInspect(context.Background(), name, types.NetworkInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			if netConfig.Ipam.Driver != "" || len(netConfig.Ipam.Config) > 0 {
				createOptions.IPAM = &network.IPAM{}

				if netConfig.Ipam.Driver != "" {
					createOptions.IPAM.Driver = netConfig.Ipam.Driver
				}

				for _, ipamConfig := range netConfig.Ipam.Config {
					config := network.IPAMConfig{
						Subnet: ipamConfig.Subnet,
					}
					createOptions.IPAM.Config = append(createOptions.IPAM.Config, config)
				}
			}
			var response types.NetworkCreateResponse
			if response, err = cli.NetworkCreate(context.Background(), name, createOptions); err != nil {
				return "", "", fmt.Errorf("failed to create network %s: %w", name, err)
			}
			networkID = response.ID
		} else {
			return "", "", err
		}
	} else {
		networkID = resource.ID
	}

	return name, networkID, nil
}

func RemoveNetworks(cli *client.Client, project string) error {
	networks, err := collectNetworks(cli, project)
	if err != nil {
		return err
	}
	for networkName, resource := range networks {
		err = destroyNetwork(cli, resource, networkName)
		if err != nil {
			return err
		}
	}
	return nil
}

func destroyNetwork(cli *client.Client, networkResources []types.NetworkResource, networkName string) error {
	for _, networkResource := range networkResources {
		fmt.Printf("Deleting network %s ... ", networkName)
		err := cli.NetworkRemove(context.Background(), networkResource.ID)
		if err != nil {
			return err
		}
		fmt.Println(networkResource.Name)
	}
	return nil
}

func collectNetworks(cli *client.Client, project string) (map[string][]types.NetworkResource, error) {
	networkList, err := cli.NetworkList(context.Background(), types.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("label", LabelProject+"="+project)),
	})
	if err != nil {
		return nil, err
	}
	networks := map[string][]types.NetworkResource{}
	for _, r := range networkList {
		resource := r.Labels[LabelNetwork]
		l, ok := networks[resource]
		if !ok {
			l = []types.NetworkResource{r}
		} else {
			l = append(l, r)
		}
		networks[resource] = l

	}
	return networks, nil
}

func NetworkMode(project string, serviceConfig compose.ServiceConfig, networks map[string]string) container.NetworkMode {
	mode := serviceConfig.NetworkMode
	if mode == "" {
		if len(networks) > 0 {
			for name := range getNetworksForService(project, serviceConfig) {
				if _, ok := networks[name]; ok {
					return container.NetworkMode(networks[name])
				}
			}
		}
		return "none"
	}
	return container.NetworkMode(mode)
}

func getNetworksForService(project string, config compose.ServiceConfig) map[string]*compose.ServiceNetworkConfig {
	if len(config.Networks) > 0 {
		return config.Networks
	}
	return map[string]*compose.ServiceNetworkConfig{fmt.Sprintf("%s-default", project): nil}
}

func BuildDefaultNetworkConfig(serviceConfig compose.ServiceConfig, networkMode container.NetworkMode) *network.NetworkingConfig {
	config := map[string]*network.EndpointSettings{}
	net := string(networkMode)
	config[net] = &network.EndpointSettings{
		Aliases: getAliases(serviceConfig.Name, serviceConfig.Networks[net], ""),
	}
	return &network.NetworkingConfig{
		EndpointsConfig: config,
	}
}

func ConnectContainerToNetworks(context context.Context, cli *client.Client,
	serviceConfig compose.ServiceConfig, containerID string, networks map[string]string) error {
	for key, net := range serviceConfig.Networks {
		config := &network.EndpointSettings{
			Aliases: getAliases(serviceConfig.Name, net, containerID),
		}
		err := cli.NetworkConnect(context, networks[key], containerID, config)
		if err != nil {
			return err
		}
	}
	return nil
}

func getAliases(serviceName string, c *compose.ServiceNetworkConfig, containerID string) []string {
	aliases := []string{serviceName}
	if containerID != "" {
		aliases = append(aliases, containerShortID(containerID))
	}
	if c != nil {
		aliases = append(aliases, c.Aliases...)
	}
	return aliases
}

func BuildContainerPortBindingsOptions(serviceConfig compose.ServiceConfig) nat.PortMap {
	bindings := nat.PortMap{}
	for _, port := range serviceConfig.Ports {
		p := nat.Port(fmt.Sprintf("%d/%s", port.Target, port.Protocol))
		bind := []nat.PortBinding{}
		binding := nat.PortBinding{}
		if port.Published > 0 {
			binding.HostPort = fmt.Sprint(port.Published)
		}
		bind = append(bind, binding)
		bindings[p] = bind
	}
	return bindings
}

func ExposedPorts(ports []compose.ServicePortConfig) nat.PortSet {
	natPorts := nat.PortSet{}
	for _, p := range ports {
		p := nat.Port(fmt.Sprintf("%d/%s", p.Target, p.Protocol))
		natPorts[p] = struct{}{}
	}
	return natPorts
}

func containerShortID(containerID string) string {
	return containerID[:12]
}
