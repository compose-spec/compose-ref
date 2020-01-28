package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"github.com/docker/compose-go/loader"
	"github.com/docker/compose-go/types"
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
		Action: func(c *cli.Context) error {
			fmt.Println("I'm alive!")

			b, err := ioutil.ReadFile(file)
			if err != nil {
				return err
			}
			config, err := loader.ParseYAML(b)
			if err != nil {
				return err
			}
			files := []types.ConfigFile{}
			files = append(files, types.ConfigFile{Filename: file, Config: config})
			_, err = loader.Load(types.ConfigDetails{
				WorkingDir:  ".",
				ConfigFiles: files,
			})
			return err
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "file",
				Value:       "compose.yaml",
				Usage:       "Compose file",
				Destination: &file,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
