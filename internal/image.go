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

func PullImageIfWithout(cli *client.Client, ctx context.Context, name string) error {
	args := filters.NewArgs(filters.Arg("reference", name))
	images, err := cli.ImageList(ctx, types.ImageListOptions{All: true, Filters: args})
	if err != nil {
		return err
	}
	if len(images) == 0 {
		fmt.Println("pulling image: " + name)
		_, err := cli.ImagePull(ctx, name, types.ImagePullOptions{})
		if err != nil {
			return err
		}

		// TODO print json message stream
	}
	return nil
}
