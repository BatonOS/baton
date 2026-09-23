// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)



type Class string

const (





	ClassSystem Class = "system"





	ClassUnknown Class = "unknown"
)




const trustedDoor = "seeded"
























const InventoryFile = ".provenance"


type entry struct {
	digest string
	door   string
}


type inventory map[string]entry




func loadInventory(path string) (inventory, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return inventory{}, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	inv := inventory{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}



		digest, rest, ok := strings.Cut(text, "  ")
		if !ok || len(digest) != 64 {
			return nil, fmt.Errorf("%s:%d is not `<sha256>  <door>  <path>`", path, line)
		}
		door, rel, ok := strings.Cut(rest, "  ")
		if !ok || door == "" || rel == "" {
			return nil, fmt.Errorf("%s:%d has no door column — a line that cannot say how a plugin "+
				"got here is a line that cannot say whether its code may run", path, line)
		}
		inv[filepath.ToSlash(rel)] = entry{digest: digest, door: door}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return inv, nil
}


func digestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}














func classify(pluginDir, idRel string, inv inventory) (Class, string) {
	onDisk := map[string]string{}

	err := filepath.WalkDir(pluginDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}



		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", p)
		}
		rel, relErr := filepath.Rel(pluginDir, p)
		if relErr != nil {
			return relErr
		}
		dg, dgErr := digestFile(p)
		if dgErr != nil {
			return dgErr
		}
		onDisk[filepath.ToSlash(filepath.Join(idRel, rel))] = dg
		return nil
	})
	if err != nil {
		return ClassUnknown, fmt.Sprintf("could not be read whole (%v)", err)
	}


	keys := make([]string, 0, len(onDisk))
	for k := range onDisk {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want, listed := inv[k]
		switch {
		case !listed:
			return ClassUnknown, fmt.Sprintf("%s is not in %s — nothing recorded how it got here", k, InventoryFile)
		case want.digest != onDisk[k]:
			return ClassUnknown, fmt.Sprintf("%s does not match the digest recorded for it", k)
		case want.door != trustedDoor:










			return ClassUnknown, fmt.Sprintf("came through the %q door, and only %q may run code", want.door, trustedDoor)
		}
	}




	missing := []string{}
	prefix := idRel + "/"
	for k := range inv {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if _, ok := onDisk[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return ClassUnknown, fmt.Sprintf("%s is in %s and not on disk", missing[0], InventoryFile)
	}

	if len(onDisk) == 0 {
		return ClassUnknown, "the directory holds no files"
	}
	return ClassSystem, fmt.Sprintf("all %d files came through the %q door and match %s",
		len(onDisk), trustedDoor, InventoryFile)
}














func ClassifyAt(root, id string) Class {
	inv, err := loadInventory(filepath.Join(root, InventoryFile))
	if err != nil {
		return ClassUnknown
	}
	c, _ := classify(filepath.Join(root, id), id, inv)
	return c
}
