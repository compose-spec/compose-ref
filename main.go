package main

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/strslice"
	"io/ioutil"
	"log"
	"os"

	"github.com/docker/compose-go/loader"
	compose "github.com/docker/compose-go/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
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

	app := &cli.App{
		Name:  "kraken",
		Usage: "Reference Compose Specification implementation",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "file",
				Aliases: []string{"f"},
				Value:       "compose.yaml",
				Usage:       "Load Compose file `FILE`",
				Destination: &file,
			},
		},
		Commands: []*cli.Command{
			{
				Name:    "up",
				Usage:   "Create and start application services",
				Action:  func(c *cli.Context) error {
					config, err := load(file)
					if err != nil {
						return err
					}
					return doUp(config)
				},
			},
			{
				Name:    "down",
				Usage:   "Stop services created by `up`",
				Action:  func(c *cli.Context) error {
					config, err := load(file)
					if err != nil {
						return err
					}
					return doDown(config)
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func doUp(config *compose.Config) error {
	cli, err := client.NewEnvClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	for _, s := range config.Services {
		fmt.Printf("Creating container for service %s ... ", s.Name)
		create, err := cli.ContainerCreate(ctx,
			&container.Config{
				Image: s.Image,
				Cmd:   strslice.StrSlice(s.Command),
				User:  s.User,
				WorkingDir: s.WorkingDir,
			},
			&container.HostConfig{
				Privileged: s.Privileged,
			},
			&network.NetworkingConfig{

			},
			"")
		if err != nil {
			return err
		}
		err = cli.ContainerStart(ctx, create.ID, types.ContainerStartOptions{})
		if err != nil {
			return err
		}
		fmt.Println(create.ID)
	}

	return nil
}

func doDown(config *compose.Config) error {
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

