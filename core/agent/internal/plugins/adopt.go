// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)





























const AdoptedDoor = "adopted"















func WriteAdoptedLedger(root, id string) error {
	dir := filepath.Join(root, id)
	lines := map[string]string{}




	if inv, err := loadInventory(filepath.Join(root, InventoryFile)); err == nil {
		for rel, e := range inv {
			if rel == id || strings.HasPrefix(rel, id+"/") {
				continue
			}
			lines[rel] = e.digest + "  " + e.door
		}
	} else {




		return fmt.Errorf("the plugin ledger is not readable, and it is not rewritten on a guess: %w", err)
	}

	walked := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}



		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link, and a link names bytes that are not in this facility", p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		lines[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:]) + "  " + AdoptedDoor
		walked++
		return nil
	})
	if err != nil {
		return err
	}
	if walked == 0 {






		return fmt.Errorf("%s unpacked to no files", id)
	}

	out := make([]string, 0, len(lines))
	for rel, v := range lines {
		out = append(out, v+"  "+rel)
	}


	sort.Strings(out)

	tmp := filepath.Join(root, InventoryFile+".tmp")
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, InventoryFile))
}














func ArchiveDigest(archive []byte) string {
	sum := sha256.Sum256(archive)
	return "sha256:" + hex.EncodeToString(sum[:])
}
