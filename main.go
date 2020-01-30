package main

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/filters"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/docker/compose-go/loader"
	compose "github.com/docker/compose-go/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/go-units"
	"github.com/urfave/cli/v2"
)

const banner = `
 .---.
(     )
 )@ @(
//|||\\
`

func main() {
	fmt.Println(banner)
	var file string
	var project string

	app := &cli.App{
		Name:  "compose-ref",
		Usage: "Reference Compose Specification implementation",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "file",
				Aliases:     []string{"f"},
				Value:       "compose.yaml",
				Usage:       "Load Compose file `FILE`",
				Destination: &file,
			},
			&cli.StringFlag{
				Name:        "project-name",
				Aliases:     []string{"n"},
				Value:       "",
				Usage:       "Set project name `NAME` (default: Compose file's folder name)",
				Destination: &project,
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "up",
				Usage: "Create and start application services",
				Action: func(c *cli.Context) error {
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
				Action: func(c *cli.Context) error {
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

func doUp(project string, config *compose.Config) error {
	cli, err := client.NewEnvClient()
	if err != nil {
		return err
	}
	return config.WithServices(nil, func(service compose.ServiceConfig) error {
		return createService(cli, project, service)
	})
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
	for k,v := range s.Labels {
		labels[k] = v
	}
	labels[LABEL_PROJECT] = project
	labels[LABEL_SERVICE] = s.Name

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

func collectContainers(cli *client.Client, project string) (map[string][]types.Container, error) {
	list, err := cli.ContainerList(context.Background(), types.ContainerListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", LABEL_PROJECT+"="+project)),
	})
	if err != nil {
		return nil, err
	}
	containers := map[string][]types.Container{}
	for _, c := range list {
		service := c.Labels[LABEL_SERVICE]
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

func doDown(project string, config *compose.Config) error {
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
	LABEL_NAMESPACE        = "io.compose-spec"
	LABEL_SERVICE          = LABEL_NAMESPACE + ".service"
	LABEL_NETWORK          = LABEL_NAMESPACE + ".network"
	LABEL_VOLUME           = LABEL_NAMESPACE + ".volume"
	LABEL_PROJECT          = LABEL_NAMESPACE + ".project"
)
