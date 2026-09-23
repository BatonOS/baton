// SPDX-License-Identifier: Apache-2.0

package core

import (
	"sort"
	"sync"
)
















var appActions = struct {
	sync.RWMutex
	byName map[string]ActionDecl
}{byName: map[string]ActionDecl{}}







func SetApps(loaded []LoadedApp) {
	byName := make(map[string]ActionDecl, len(loaded))
	for _, a := range loaded {
		byName[a.Decl.Name] = a.Decl
	}
	appActions.Lock()
	appActions.byName = byName
	appActions.Unlock()
}







func lookupAction(name string) (ActionDecl, bool) {
	if d, ok := declared[name]; ok {
		return d, true
	}
	appActions.RLock()
	d, ok := appActions.byName[name]
	appActions.RUnlock()
	return d, ok
}




func actionNames() []string {
	appActions.RLock()
	names := make([]string, 0, len(declared)+len(appActions.byName))
	for name := range appActions.byName {
		names = append(names, name)
	}
	appActions.RUnlock()
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
