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
	"path/filepath"
	"strings"

	compose "github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
)

func GetVolumesFromConfig(cli *client.Client, project string, config *compose.Config) error {
	for defaultVolumeName, volumeConfig := range config.Volumes {
		err := CreateVolume(cli, project, defaultVolumeName, volumeConfig)
		if err != nil {
			return err
		}
	}
	return nil
}

var fakeConfigBindings = make(map[string]string) // Mapping on Map[ConfigName]File
func GetConfigsFromConfig(prjDir string, config *compose.Config) error {
	for k, v := range config.Configs {
		name := k
		if v.Name != "" {
			name = v.Name
		}
		fakeConfigBindings[name] = v.File
	}
	return nil
}

var fakeSecretBindings = make(map[string]string) // Mapping on Map[SecretName]File
func GetSecretsFromConfig(prjDir string, config *compose.Config) error {
	for k, v := range config.Secrets {
		name := k
		if v.Name != "" {
			name = v.Name
		}
		fakeSecretBindings[name] = v.File
	}
	return nil
}

func CreateVolume(cli *client.Client, project string, volumeDefaultName string, volumeConfig compose.VolumeConfig) error {
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
			LabelProject: project,
			LabelVolume:  name,
		},
	})

	return err
}

func RemoveVolumes(cli *client.Client, project string) error {
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

func collectVolumes(cli *client.Client, project string) (map[string][]types.Volume, error) {
	filter := filters.NewArgs(filters.Arg("label", LabelProject+"="+project))
	list, err := cli.VolumeList(context.Background(), filter)
	if err != nil {
		return nil, err
	}
	volumes := map[string][]types.Volume{}
	for _, v := range list.Volumes {
		resource := v.Labels[LabelVolume]
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

func CreateContainerConfigMounts(s compose.ServiceConfig, prjDir string) ([]mount.Mount, error) {
	var fileRefs []compose.FileReferenceConfig
	for _, f := range s.Configs {
		fileRefs = append(fileRefs, compose.FileReferenceConfig(f))
	}
	return createFakeMounts(fileRefs, fakeConfigBindings, prjDir)
}

func CreateContainerSecretMounts(s compose.ServiceConfig, prjDir string) ([]mount.Mount, error) {
	var fileRefs []compose.FileReferenceConfig
	for _, f := range s.Secrets {
		fileRefs = append(fileRefs, compose.FileReferenceConfig(f))
	}
	return createFakeMounts(fileRefs, fakeSecretBindings, prjDir)
}

func createFakeMounts(fileRefs []compose.FileReferenceConfig, fakeBindings map[string]string, prjDir string) ([]mount.Mount, error) {
	var mounts []mount.Mount
	for _, v := range fileRefs {
		source, ok := fakeBindings[v.Source]
		if !ok {
			return nil, fmt.Errorf("couldn't find reference %q", v.Source)
		}
		target := v.Target
		if target == "" {
			target = filepath.Join("/", source)
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(prjDir, source)
		}
		mounts = append(mounts, mount.Mount{
			Type:        compose.VolumeTypeBind,
			Source:      source,
			Target:      target,
			ReadOnly:    true,
			Consistency: mount.ConsistencyDefault,
		})
	}
	return mounts, nil
}

func CreateContainerMounts(s compose.ServiceConfig, prjDir string) ([]mount.Mount, error) {
	var mounts []mount.Mount
	for _, v := range s.Volumes {
		source := v.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(prjDir, source)
		}
		mounts = append(mounts, mount.Mount{
			Type:          mount.Type(v.Type),
			Source:        source,
			Target:        v.Target,
			ReadOnly:      v.ReadOnly,
			Consistency:   mount.Consistency(v.Consistency),
			BindOptions:   buildBindOption(v.Bind),
			VolumeOptions: buildVolumeOptions(v.Volume),
			TmpfsOptions:  buildTmpfsOptions(v.Tmpfs),
		})
	}
	return mounts, nil
}

func buildBindOption(bind *compose.ServiceVolumeBind) *mount.BindOptions {
	if bind == nil {
		return nil
	}
	return &mount.BindOptions{
		Propagation: mount.Propagation(bind.Propagation),
	}
}

func buildVolumeOptions(vol *compose.ServiceVolumeVolume) *mount.VolumeOptions {
	if vol == nil {
		return nil
	}
	return &mount.VolumeOptions{
		NoCopy: vol.NoCopy,
	}
}

func buildTmpfsOptions(tmpfs *compose.ServiceVolumeTmpfs) *mount.TmpfsOptions {
	if tmpfs == nil {
		return nil
	}
	return &mount.TmpfsOptions{
		SizeBytes: tmpfs.Size,
	}
}
