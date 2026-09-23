// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)








const AppsDirName = "apps"








const TransactionsDirName = "transactions"


const AppDeclFileName = "action.json"



type LoadedApp struct {
	AppID string
	Decl  ActionDecl


	SHA256 string
}















func LoadApps(ctx context.Context, dataDir string, knownSteps KnownStepsFunc) ([]LoadedApp, []error) {
	root := filepath.Join(dataDir, AppsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {


			return nil, nil
		}
		return nil, []error{fmt.Errorf("apps directory %s: %w", root, err)}
	}

	var loaded []LoadedApp
	var refused []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		path := filepath.Join(root, appID, AppDeclFileName)
		raw, err := os.ReadFile(path)
		if err != nil {
			refused = append(refused, fmt.Errorf("app %q: %w", appID, err))
			continue
		}
		a, err := DecodeAppDeclaration(raw)
		if err != nil {
			refused = append(refused, fmt.Errorf("app %q (%s): %w", appID, path, err))
			continue
		}
		if err := RegisterApp(ctx, appID, a, knownSteps); err != nil {
			refused = append(refused, err)
			continue
		}
		sum := sha256.Sum256(raw)
		loaded = append(loaded, LoadedApp{
			AppID: appID, SHA256: hex.EncodeToString(sum[:]),
			Decl: a.ActionDecl(appID, hex.EncodeToString(sum[:])),
		})
	}



	sort.Slice(loaded, func(i, j int) bool { return loaded[i].AppID < loaded[j].AppID })
	return loaded, refused
}








func AskExecutors(apps map[string]*ProviderClient) KnownStepsFunc {
	return func(ctx context.Context, appID string) ([]string, error) {
		c := apps[appID]
		if c == nil {
			return nil, fmt.Errorf("no executor is wired for app %q", appID)
		}
		return c.KnownSteps(ctx)
	}
}







const AppExecutorSocketName = "executor.sock"







func AppClients(dataDir string) map[string]*ProviderClient {
	entries, err := os.ReadDir(filepath.Join(dataDir, AppsDirName))
	if err != nil {
		return map[string]*ProviderClient{}
	}
	out := make(map[string]*ProviderClient, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out[e.Name()] = NewAppClient(e.Name(), filepath.Join(dataDir, AppsDirName, e.Name(), AppExecutorSocketName))
	}
	return out
}

