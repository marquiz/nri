/*
   Copyright The containerd Authors.

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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/log"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
)

var (
	pluginName string
	pluginIdx  string
	kubeconfig string
	configmap  string
	configfile string
	opts       []stub.Option
)

func main() {
	l := logrus.StandardLogger()
	l.SetFormatter(&logrus.TextFormatter{
		PadLevelText: true,
	})

	flag.StringVar(&pluginName, "name", "noderesourceupdater", "plugin name to register to NRI")
	flag.StringVar(&pluginIdx, "idx", "99", "plugin index to register to NRI")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig file to use")
	flag.StringVar(&configmap, "configmap", "", "configmap to watch")
	flag.StringVar(&configfile, "config", "", "file to watch")
	flag.Parse()
	ctx := log.WithLogger(context.Background(), l.WithField("name", pluginName).WithField("idx", pluginIdx))

	if pluginName != "" {
		opts = append(opts, stub.WithPluginName(pluginName))
	}
	if pluginIdx != "" {
		opts = append(opts, stub.WithPluginIdx(pluginIdx))
	}

	p := &plugin{l: log.G(ctx)}
	var err error
	if p.stub, err = stub.New(p, opts...); err != nil {
		log.G(ctx).Fatalf("failed to create plugin stub: %v", err)
	}

	// Start plugin stub
	if err = p.stub.Start(ctx); err != nil {
		log.G(ctx).Fatalf("failed to start plugin stub: %v", err)
	}

	// Start watcher
	switch {
	case configfile != "" && configmap != "":
		log.G(ctx).Fatalf("only one of -config and -configmap may be specified")
	case configmap != "":
		if err := p.startConfigMapWatcher(ctx, kubeconfig); err != nil {
			log.G(ctx).Fatalf("failed to start configmap watcher: %v", err)
		}
	case configfile != "":
		if err := p.startFileWatcher(ctx, configfile); err != nil {
			log.G(ctx).Fatalf("failed to start file watcher: %v", err)
		}
	default:
		log.G(ctx).Fatalf("one of -config or -configmap must be specified")
	}

	// TODO: we don't get possible errors
	p.stub.Wait()
}

type plugin struct {
	stub stub.Stub
	l    *logrus.Entry
}

type ResourceConfig struct {
	CPUsPerCore    int64 `json:"cpusPerCore"`
	CoresPerSocket int64 `json:"coresPerSocket"`
	MemPerSocket   int64 `json:"memPerSocket"`
	NumSockets     int64 `json:"numSockets"`
	SwapSize       int64 `json:"swapSize"`
}

func defaultResourceConfig() ResourceConfig {
	return ResourceConfig{
		CPUsPerCore:    1,
		CoresPerSocket: 2,
		MemPerSocket:   1024 * 1024 * 1024,
		NumSockets:     1,
		SwapSize:       0,
	}
}

func sanitizeResourceConfig(resourceConfig *ResourceConfig) {
	if resourceConfig.CPUsPerCore < 1 {
		resourceConfig.CPUsPerCore = 1
	}
	if resourceConfig.CPUsPerCore > 16 {
		resourceConfig.CPUsPerCore = 16
	}
	if resourceConfig.CoresPerSocket < 1 {
		resourceConfig.CoresPerSocket = 1
	}
	if resourceConfig.CoresPerSocket > 512 {
		resourceConfig.CoresPerSocket = 512
	}
	if resourceConfig.MemPerSocket < 1024*1024 {
		resourceConfig.MemPerSocket = 1024 * 1024
	}
	if resourceConfig.NumSockets < 1 {
		resourceConfig.NumSockets = 1
	}
	if resourceConfig.NumSockets > 8 {
		resourceConfig.NumSockets = 8
	}
	if resourceConfig.SwapSize < 0 {
		resourceConfig.SwapSize = 0
	}
}

func newUpdateNodeResourcesRequest(resourceConfig ResourceConfig) *api.UpdateNodeResourcesRequest {
	sanitizeResourceConfig(&resourceConfig)

	zones := []*api.ResourceTopologyZone{}

	rootZ := api.ResourceTopologyZone{
		Name: "System",
		Type: api.ResourceTopologyZoneSystem,
		Resources: []*api.ResourceTopologyResourceInfo{
			{
				Name:     "swap",
				Capacity: fmt.Sprintf("%d", resourceConfig.SwapSize),
			},
		},
	}
	zones = append(zones, &rootZ)

	cpuID := int64(0)
	for packageID := int64(0); packageID < resourceConfig.NumSockets; packageID++ {
		numaNodeCPUIDs := []int64{cpuID, 0}

		packageZ := api.ResourceTopologyZone{
			Name:   fmt.Sprintf("Package-%d", packageID),
			Type:   api.ResourceTopologyZonePackage,
			Parent: rootZ.Name,
		}
		zones = append(zones, &packageZ)

		// Add cores
		for coreID := int64(0); coreID < resourceConfig.CoresPerSocket; coreID++ {
			cpuIDs := fmt.Sprintf("%d", cpuID)
			if resourceConfig.CPUsPerCore > 1 {
				cpuIDs = fmt.Sprintf("%d-%d", cpuID, cpuID+resourceConfig.CPUsPerCore-1)
			}

			coreZ := api.ResourceTopologyZone{
				Name:   fmt.Sprintf("Core-%d.%d", packageID, coreID),
				Type:   api.ResourceTopologyZoneCore,
				Parent: packageZ.Name,
				Resources: []*api.ResourceTopologyResourceInfo{
					{
						Name:     string(corev1.ResourceCPU),
						Capacity: fmt.Sprintf("%d", resourceConfig.CPUsPerCore),
					},
				},
				Attributes: map[string]string{
					"cpu-ids": cpuIDs,
				},
			}
			zones = append(zones, &coreZ)

			cpuID += resourceConfig.CPUsPerCore
			numaNodeCPUIDs[1] = cpuID - 1
		}

		// Add NUMA node (one per package)
		numaNodeZ := api.ResourceTopologyZone{
			Name:   fmt.Sprintf("Node-%d", packageID),
			Type:   api.ResourceTopologyZoneNUMANode,
			Parent: packageZ.Name,
			Resources: []*api.ResourceTopologyResourceInfo{
				{
					Name:     string(corev1.ResourceMemory),
					Capacity: fmt.Sprintf("%d", resourceConfig.MemPerSocket),
				},
			},
			Attributes: map[string]string{
				"cpu-ids": fmt.Sprintf("%d-%d", numaNodeCPUIDs[0], numaNodeCPUIDs[1]),
			},
		}

		costs := make([]*api.ResourceTopologyCost, resourceConfig.NumSockets)
		for i := int64(0); i < resourceConfig.NumSockets; i++ {
			distance := uint32(0)
			if i != packageID {
				distance = 10
			}
			costs[i] = &api.ResourceTopologyCost{
				Name:  fmt.Sprintf("Node-%d", i),
				Value: distance,
			}
		}
		numaNodeZ.Costs = costs

		zones = append(zones, &numaNodeZ)
	}

	return &api.UpdateNodeResourcesRequest{
		ResourceTopology: &api.ResourceTopology{
			Zones: zones,
		},
	}
}

func (p *plugin) startFileWatcher(ctx context.Context, configfile string) error {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %v", err)
	}

	go func() {
		defer fsWatcher.Close()
		cleanPath := filepath.Clean(configfile)
		ratelimit := time.After(0)
		for {
			select {
			case event, ok := <-fsWatcher.Events:
				if !ok {
					p.l.Infof("fsnotify events channel closed")
					return
				}
				if filepath.Clean(event.Name) != cleanPath {
					continue
				}
				p.l.Infof("fsnotify event %v", event)
				ratelimit = time.After(200 * time.Millisecond)

			case <-ratelimit:
				// Prevent multiple updates in a short period of time because a flood of fsnotify events (rename, write, create etc.)
				p.updateNodeResourcesFromFile(configfile)
			case err, ok := <-fsWatcher.Errors:
				if !ok {
					p.l.Infof("fsnotify errors channel closed")
					return
				}
				p.l.Errorf("fsnotify error: %v", err)
			}
		}
	}()

	if err := fsWatcher.Add(filepath.Dir(configfile)); err != nil {
		return fmt.Errorf("failed to watch config file %s: %v", configfile, err)
	}

	p.l.Infof("Watching config file %s", configfile)

	return nil
}

func (p *plugin) updateNodeResourcesRequestFromConfig(data []byte) {
	resourceConfig := defaultResourceConfig()

	// Parse YAML data
	err := yaml.Unmarshal(data, &resourceConfig)
	if err != nil {
		p.l.Errorf("Error unmarshaling resource config: %v", err)
		return
	}

	// Send update request
	p.l.Infof("updating node resources with resource config: %+v", resourceConfig)
	req := newUpdateNodeResourcesRequest(resourceConfig)

	if err := p.stub.UpdateNodeResources(req); err != nil {
		p.l.Errorf("Failed to update node resources: %v", err)
	}
}

func (p *plugin) updateNodeResourcesFromFile(configfile string) {
	data, err := os.ReadFile(configfile)
	if err != nil {
		if os.IsNotExist(err) {
			p.l.Infof("Config file %s not found", configfile)
		} else {
			p.l.Errorf("Failed to read config file %s: %v", configfile, err)
		}
		p.l.Infof("Using default resource config")
		data = nil
	}
	p.updateNodeResourcesRequestFromConfig(data)
}
