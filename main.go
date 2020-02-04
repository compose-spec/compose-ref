package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/errdefs"
	"gopkg.in/yaml.v2"

	"github.com/compose-spec/compose-go/loader"
	compose "github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
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
	fmt.Print(banner)
	var file string
	var project string

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

	for defaultNetworkName, networkConfig := range config.Networks {
		err = createNetwork(cli, project, defaultNetworkName, networkConfig)
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
			return createService(cli, project, service)
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

		// Some container exist for service but with an obsolete configuration. We need to replace them
		err = removeContainers(cli, containers)
		if err != nil {
			return err
		}
		return createService(cli, project, service)
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
			fmt.Printf("Stopping container for service %s ... ", serviceName)
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

func createService(cli *client.Client, project string, s compose.ServiceConfig) error {
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
		},
		&container.HostConfig{
			NetworkMode:    container.NetworkMode(s.NetworkMode),
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
		},
		&network.NetworkingConfig{},
		"")
	if err != nil {
		return err
	}
	err = cli.ContainerStart(ctx, create.ID, types.ContainerStartOptions{})
	if err != nil {
		return err
	}
	fmt.Println(create.ID)
	return nil
}

func createNetwork(cli *client.Client, project string, networkDefaultName string, netConfig compose.NetworkConfig) error {
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
			return fmt.Errorf("Network %s declared as external, but could not be found. "+
				"Please create the network manually using `docker network create %s` and try again",
				name, name)
		}
	}

	_, err := cli.NetworkInspect(context.Background(), name, types.NetworkInspectOptions{})
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
			if _, err := cli.NetworkCreate(context.Background(), name, createOptions); err != nil {
				return fmt.Errorf("failed to create network %s: %w", name, err)
			}
		} else {
			return err
		}
	}

	return nil
}

func collectContainers(cli *client.Client, project string) (map[string][]types.Container, error) {
	list, err := cli.ContainerList(context.Background(), types.ContainerListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelProject+"="+project)),
	})
	if err != nil {
		return nil, err
	}
	containers := map[string][]types.Container{}
	for _, c := range list {
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
	list, err := cli.NetworkList(context.Background(), types.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("label", labelProject+"="+project)),
	})
	if err != nil {
		return nil, err
	}
	networks := map[string][]types.NetworkResource{}
	for _, r := range list {
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

func doDown(project string, config *compose.Config) error {
	cli, err := getClient()
	if err != nil {
		return nil
	}
	err = removeServices(cli, project)
	if err != nil {
		return err
	}

	return destroyNetworks(cli, project)
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

const (
	labelNamespace = "io.compose-spec"
	labelService   = labelNamespace + ".service"
	labelNetwork   = labelNamespace + ".network"
	labelVolume    = labelNamespace + ".volume"
	labelProject   = labelNamespace + ".project"
	labelConfig    = labelNamespace + ".config"
)
