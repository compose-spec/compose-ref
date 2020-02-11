package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
	"gopkg.in/yaml.v2"

	"github.com/compose-spec/compose-go/loader"
	compose "github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-units"
	commandLine "github.com/urfave/cli/v2"
)

const banner = `
 .---.
(     )
 )@ @(
//|||\\

`

func main() {
	var file string
	var project string

	fmt.Print(banner)
	app := &commandLine.App{
		Name:  "compose-ref",
		Usage: "Reference Compose Specification implementation",
		Flags: []commandLine.Flag{
			&commandLine.StringFlag{
				Name:        "file",
				Aliases:     []string{"f"},
				Value:       "compose.yaml",
				Usage:       "Load Compose file `FILE`",
				Destination: &file,
			},
			&commandLine.StringFlag{
				Name:        "project-name",
				Aliases:     []string{"n"},
				Value:       "",
				Usage:       "Set project name `NAME` (default: Compose file's folder name)",
				Destination: &project,
			},
		},
		Commands: []*commandLine.Command{
			{
				Name:  "up",
				Usage: "Create and start application services",
				Action: func(c *commandLine.Context) error {
					config, err := load(file)
					if err != nil {
						return err
					}
					project, err = getProject(project, file)
					if err != nil {
						return err
					}

					return doUp(project, config)
				},
			},
			{
				Name:  "down",
				Usage: "Stop services created by `up`",
				Action: func(c *commandLine.Context) error {
					config, err := load(file)
					if err != nil {
						return err
					}
					project, err = getProject(project, file)
					if err != nil {
						return err
					}
					return doDown(project, config)
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func getProject(project string, file string) (string, error) {
	if project == "" {
		abs, err := filepath.Abs(file)
		if err != nil {
			return "", err
		}
		project = filepath.Base(filepath.Dir(abs))
	}
	return project, nil
}

func getClient() (*client.Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}
	cli.NegotiateAPIVersion(context.Background())
	return cli, nil
}

func doUp(project string, config *compose.Config) error {
	cli, err := getClient()
	if err != nil {
		return err
	}
	networks := map[string]string{}
	for defaultNetworkName, networkConfig := range config.Networks {
		name, id, err := createNetwork(cli, project, defaultNetworkName, networkConfig)
		if err != nil {
			return err
		}
		networks[name] = id
	}

	for defaultVolumeName, volumeConfig := range config.Volumes {
		err = createVolume(cli, project, defaultVolumeName, volumeConfig)
		if err != nil {
			return err
		}
	}

	observedState, err := collectContainers(cli, project)
	if err != nil {
		return err
	}

	err = config.WithServices(nil, func(service compose.ServiceConfig) error {
		containers := observedState[service.Name]
		delete(observedState, service.Name)

		// If no container is set for the service yet, then we just need to create them
		if len(containers) == 0 {
			return createService(cli, project, service, networks)
		}

		// We compare container config stored as plain yaml in a label with expected one
		b, err := yaml.Marshal(service)
		if err != nil {
			return err
		}
		expected := string(b)

		diverged := false
		for _, cntr := range containers {
			config := cntr.Labels[labelConfig]
			if config != expected {
				diverged = true
				break
			}
		}

		if !diverged {
			// Existing containers are up-to-date with the Compose file configuration, so just keep them running
			return nil
		}

		// Some containers exist for service but with an obsolete configuration. We need to replace them
		err = removeContainers(cli, containers)
		if err != nil {
			return err
		}
		return createService(cli, project, service, networks)
	})

	if err != nil {
		return err
	}

	// Remaining containers in observed state don't have a matching service in Compose file => orphaned to be removed
	for _, orphaned := range observedState {
		err = removeContainers(cli, orphaned)
		if err != nil {
			return err
		}
	}
	return nil
}

func removeContainers(cli *client.Client, containers []types.Container) error {
	ctx := context.Background()
	for _, c := range containers {
		if serviceName, ok := c.Labels[labelService]; ok {
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

func createService(cli *client.Client, project string, s compose.ServiceConfig, networks map[string]string) error {
	ctx := context.Background()

	var shmSize int64
	if s.ShmSize != "" {
		v, err := units.RAMInBytes(s.ShmSize)
		if err != nil {
			return err
		}
		shmSize = v
	}

	labels := map[string]string{}
	for k, v := range s.Labels {
		labels[k] = v
	}
	labels[labelProject] = project
	labels[labelService] = s.Name

	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	labels[labelConfig] = string(b)

	fmt.Printf("Creating container for service %s ... ", s.Name)
	networkMode := networkMode(s, networks)
	create, err := cli.ContainerCreate(ctx,
		&container.Config{
			Hostname:        s.Hostname,
			Domainname:      s.DomainName,
			User:            s.User,
			Tty:             s.Tty,
			OpenStdin:       s.StdinOpen,
			Cmd:             strslice.StrSlice(s.Command),
			Image:           s.Image,
			Labels:          labels,
			WorkingDir:      s.WorkingDir,
			Entrypoint:      strslice.StrSlice(s.Entrypoint),
			NetworkDisabled: s.NetworkMode == "disabled",
			MacAddress:      s.MacAddress,
			StopSignal:      s.StopSignal,
			ExposedPorts:    exposedPorts(s.Ports),
		},
		&container.HostConfig{
			NetworkMode:    networkMode,
			RestartPolicy:  container.RestartPolicy{Name: s.Restart},
			CapAdd:         s.CapAdd,
			CapDrop:        s.CapDrop,
			DNS:            s.DNS,
			DNSSearch:      s.DNSSearch,
			ExtraHosts:     s.ExtraHosts,
			IpcMode:        container.IpcMode(s.Ipc),
			Links:          s.Links,
			PidMode:        container.PidMode(s.Pid),
			Privileged:     s.Privileged,
			ReadonlyRootfs: s.ReadOnly,
			SecurityOpt:    s.SecurityOpt,
			UsernsMode:     container.UsernsMode(s.UserNSMode),
			ShmSize:        shmSize,
			Sysctls:        s.Sysctls,
			Isolation:      container.Isolation(s.Isolation),
			Init:           s.Init,
			PortBindings:   buildContainerBindingOptions(s),
		},
		buildDefaultNetworkConfig(s, networkMode),
		"")
	if err != nil {
		return err
	}
	for key, net := range s.Networks {
		config := &network.EndpointSettings{
			Aliases: getAliases(s.Name, net),
		}
		err = cli.NetworkConnect(ctx, networks[key], create.ID, config)
		if err != nil {
			return err
		}
	}
	err = cli.ContainerStart(ctx, create.ID, types.ContainerStartOptions{})
	if err != nil {
		return err
	}
	fmt.Println(create.ID)
	return nil
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
	createOptions.Labels[labelProject] = project
	createOptions.Labels[labelNetwork] = name

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

func createVolume(cli *client.Client, project string, volumeDefaultName string, volumeConfig compose.VolumeConfig) error {
	name := volumeDefaultName
	if volumeConfig.Name != "" {
		name = volumeConfig.Name
	}
	volumeID := fmt.Sprintf("%s_%s", strings.Trim(project, "-_"), name)

	ctx := context.Background()

	// If volume already exists, return here
	if volumeConfig.External.Name != "" {
		fmt.Printf("Volume %s declared as external. No new volume will be created.\n", name)
	}
	_, err := cli.VolumeInspect(ctx, volumeID)
	if err == nil {
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return err
	}

	// If volume is marked as external but doesn't already exist then return an error
	if volumeConfig.External.Name != "" {
		return fmt.Errorf("Volume %s declared as external, but could not be found. "+
			"Please create the volume manually using `docker volume create --name=%s` and try again.\n", name, name)
	}

	fmt.Printf("Creating volume %q with %s driver\n", name, volumeConfig.Driver)
	_, err = cli.VolumeCreate(ctx, volume.VolumeCreateBody{
		Name:       volumeID,
		Driver:     volumeConfig.Driver,
		DriverOpts: volumeConfig.DriverOpts,
		Labels: map[string]string{
			labelProject: project,
			labelVolume:  name,
		},
	})

	return err
}

func collectContainers(cli *client.Client, project string) (map[string][]types.Container, error) {
	containerList, err := cli.ContainerList(context.Background(), types.ContainerListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelProject+"="+project)),
	})
	if err != nil {
		return nil, err
	}
	containers := map[string][]types.Container{}
	for _, c := range containerList {
		service := c.Labels[labelService]
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

func collectNetworks(cli *client.Client, project string) (map[string][]types.NetworkResource, error) {
	networkList, err := cli.NetworkList(context.Background(), types.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("label", labelProject+"="+project)),
	})
	if err != nil {
		return nil, err
	}
	networks := map[string][]types.NetworkResource{}
	for _, r := range networkList {
		resource := r.Labels[labelNetwork]
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

func collectVolumes(cli *client.Client, project string) (map[string][]types.Volume, error) {
	filter := filters.NewArgs(filters.Arg("label", labelProject+"="+project))
	list, err := cli.VolumeList(context.Background(), filter)
	if err != nil {
		return nil, err
	}
	volumes := map[string][]types.Volume{}
	for _, v := range list.Volumes {
		resource := v.Labels[labelVolume]
		l, ok := volumes[resource]
		if !ok {
			l = []types.Volume{*v}
		} else {
			l = append(l, *v)
		}
		volumes[resource] = l
	}
	return volumes, nil
}

func doDown(project string, config *compose.Config) error {
	cli, err := getClient()
	if err != nil {
		return nil
	}
	err = removeServices(cli, project)
	if err != nil {
		return err
	}
	err = destroyVolumes(cli, project)
	if err != nil {
		return err
	}
	err = destroyNetworks(cli, project)
	if err != nil {
		return err
	}
	return nil
}

func removeServices(cli *client.Client, project string) error {
	containers, err := collectContainers(cli, project)
	if err != nil {
		return err
	}

	for _, replicaList := range containers {
		err = removeContainers(cli, replicaList)
		if err != nil {
			return err
		}
	}
	return nil
}

func destroyNetworks(cli *client.Client, project string) error {
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

func destroyVolumes(cli *client.Client, project string) error {
	volumes, err := collectVolumes(cli, project)
	if err != nil {
		return err
	}
	for volumeName, volume := range volumes {
		err = destroyVolume(cli, volume, volumeName)
		if err != nil {
			return err
		}
	}
	return nil
}

func destroyVolume(cli *client.Client, volume []types.Volume, volumeName string) error {
	ctx := context.Background()
	for _, v := range volume {
		fmt.Printf("Deleting volume %s ... ", volumeName)
		err := cli.VolumeRemove(ctx, v.Name, false)
		if err != nil {
			return err
		}
		fmt.Println(v.Name)
	}
	return nil
}

func load(file string) (*compose.Config, error) {
	b, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}
	config, err := loader.ParseYAML(b)
	if err != nil {
		return nil, err
	}
	files := []compose.ConfigFile{}
	files = append(files, compose.ConfigFile{Filename: file, Config: config})
	return loader.Load(compose.ConfigDetails{
		WorkingDir:  ".",
		ConfigFiles: files,
	})
}

func networkMode(serviceConfig compose.ServiceConfig, networks map[string]string) container.NetworkMode {
	mode := serviceConfig.NetworkMode
	if mode == "" {
		if len(networks) > 0 {
			for name := range getNetworksForService(serviceConfig) {
				if _, ok := networks[name]; ok {
					return container.NetworkMode(networks[name])
				}
			}
		}
		return "none"
	}
	return container.NetworkMode(mode)
}

func getNetworksForService(config compose.ServiceConfig) map[string]*compose.ServiceNetworkConfig {
	if len(config.Networks) > 0 {
		return config.Networks
	}
	return map[string]*compose.ServiceNetworkConfig{"default": nil}
}

func exposedPorts(ports []compose.ServicePortConfig) nat.PortSet {
	natPorts := nat.PortSet{}
	for _, p := range ports {
		p := nat.Port(fmt.Sprintf("%d/%s", p.Target, p.Protocol))
		natPorts[p] = struct{}{}
	}
	return natPorts
}

func getAliases(serviceName string, c *compose.ServiceNetworkConfig) []string {
	aliases := []string{serviceName}
	if c != nil {
		aliases = append(aliases, c.Aliases...)
	}
	return aliases
}

func buildDefaultNetworkConfig(serviceConfig compose.ServiceConfig, networkMode container.NetworkMode) *network.NetworkingConfig {
	config := map[string]*network.EndpointSettings{}
	net := string(networkMode)
	config[net] = &network.EndpointSettings{
		Aliases: getAliases(serviceConfig.Name, serviceConfig.Networks[net]),
	}

	return &network.NetworkingConfig{
		EndpointsConfig: config,
	}
}

func buildContainerBindingOptions(serviceConfig compose.ServiceConfig) nat.PortMap {
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

const (
	labelNamespace = "io.compose-spec"
	labelService   = labelNamespace + ".service"
	labelNetwork   = labelNamespace + ".network"
	labelVolume    = labelNamespace + ".volume"
	labelProject   = labelNamespace + ".project"
	labelConfig    = labelNamespace + ".config"
	FAIL_LINT      = "hbhb"
)
