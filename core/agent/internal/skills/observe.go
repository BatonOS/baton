// SPDX-License-Identifier: Apache-2.0

package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)







var ErrNoSuchPackage = errors.New("skills: no package by that name on this node")












type Observation struct {

	Name string


	ContentDigest string



	CodeID string
}






func Observe(mountPath, name string) (Observation, error) {
	if mountPath == "" {
		return Observation{}, ErrNoMountPath
	}
	dir, err := (&Syncer{MountPath: mountPath}).dirFor(name)
	if err != nil {
		return Observation{}, err
	}
	if _, err := os.Stat(dir); err != nil {
		return Observation{}, fmt.Errorf("%w: %s", ErrNoSuchPackage, name)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {



		return Observation{}, fmt.Errorf("%s has no SKILL.md, so it has no content digest", name)
	}
	return Observation{Name: name, ContentDigest: ContentDigest(raw), CodeID: ReadCode(raw)}, nil
}
