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
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/containerd/log"
	"github.com/sirupsen/logrus"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
)

type plugin struct {
	stub          stub.Stub
	l             *logrus.Logger
	enabledFields map[string]bool
}

type fieldHandlerFunc func(ctx context.Context, adjustment *api.ContainerAdjustment, value string) error

var fieldHandlers = map[string]fieldHandlerFunc{
	"remove":           removeRdt,
	"closid":           adjustClosID,
	"schemata":         adjustSchemata,
	"enablemonitoring": adjustEnableMonitoring,
}

var fieldHandlerNames = slices.Sorted(maps.Keys(fieldHandlers))

func main() {
	p := &plugin{l: logrus.StandardLogger()}
	p.l.SetFormatter(&logrus.TextFormatter{
		PadLevelText: true,
	})

	pluginIdx := flag.String("idx", "", "plugin index to register to NRI")
	flag.Func("allowed-fields", "comma-separated list rdt fields that the plugin is allowed to modify ("+strings.Join(fieldHandlerNames, ", ")+", all; default: all)", func(s string) error {
		p.parseAllowedFields(s)
		return nil
	})
	verbose := flag.Bool("verbose", false, "enable verbose logging")

	flag.Parse()
	p.l.WithField("verbose", *verbose).Info("Starting plugin")

	if *verbose {
		p.l.SetLevel(logrus.DebugLevel)
	}

	opts := []stub.Option{}
	if *pluginIdx != "" {
		opts = append(opts, stub.WithPluginIdx(*pluginIdx))
	}

	var err error
	if p.stub, err = stub.New(p, opts...); err != nil {
		p.l.Fatalf("Failed to create plugin stub: %v", err)
	}

	if err := p.stub.Run(context.Background()); err != nil {
		p.l.Errorf("Plugin exited with error %v", err)
		os.Exit(1)
	}
}

func (p *plugin) parseAllowedFields(s string) {
	enabled := map[string]bool{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if f == "all" {
			for name := range fieldHandlers {
				enabled[name] = true
			}
			continue
		}
		if _, ok := fieldHandlers[f]; ok {
			enabled[f] = true
		} else {
			p.l.WithField("field", f).Info("Unknown rdt field in -allowed-fields, must be one of: all, ", strings.Join(fieldHandlerNames, ", "))
		}
	}
	p.enabledFields = enabled
}

func (p *plugin) CreateContainer(ctx context.Context, pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	l := logrus.NewEntry(p.l)
	if pod != nil {
		l = l.WithFields(logrus.Fields{"namespace": pod.Namespace, "pod": pod.Name})
	}
	if container != nil {
		l = l.WithField("container", container.Name)
	}
	ctx = log.WithLogger(ctx, l)
	log.G(ctx).Debug("Create container")

	adjustment := &api.ContainerAdjustment{}
	err := p.adjustRdt(ctx, adjustment, container.Name, pod.Annotations)
	if err != nil {
		return nil, nil, err
	}
	return adjustment, nil, nil
}

func (p *plugin) adjustRdt(ctx context.Context, adjustment *api.ContainerAdjustment, container string, annotations map[string]string) error {
	rdtAnnotationKeySuffix := ".rdt.noderesource.dev/container." + container

	type handler struct {
		name  string
		fn    fieldHandlerFunc
		value string
	}
	handlers := []handler{}
	for k, v := range annotations {
		if strings.HasSuffix(k, rdtAnnotationKeySuffix) {
			fieldName := strings.TrimSuffix(k, rdtAnnotationKeySuffix)
			if p.enabledFields[fieldName] {
				handlers = append(handlers, handler{name: fieldName, fn: fieldHandlers[fieldName], value: v})
			} else {
				if _, ok := fieldHandlers[fieldName]; ok {
					log.G(ctx).WithField("field_name", fieldName).Info("Field not allowed to be modified (disabled with -allowed-fields)")
				} else {
					log.G(ctx).WithField("field_name", fieldName).Info("Unknown rdt field")
					continue
				}
			}
		}
	}

	sort.Slice(handlers, func(i, j int) bool {
		if handlers[i].name == "remove" {
			return true
		}
		if handlers[j].name == "remove" {
			return false
		}
		return handlers[i].name < handlers[j].name
	})

	for _, h := range handlers {
		if err := h.fn(ctx, adjustment, h.value); err != nil {
			return err
		}
	}

	return nil
}

func removeRdt(ctx context.Context, adjustment *api.ContainerAdjustment, value string) error {
	remove, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	if remove {
		log.G(ctx).Info("Remove RDT config")
		adjustment.RemoveLinuxRDT()
	}
	return nil
}

func adjustClosID(ctx context.Context, adjustment *api.ContainerAdjustment, value string) error {
	log.G(ctx).WithField("closid", value).Info("Adjust closid")
	adjustment.SetLinuxRDTClosID(value)
	return nil
}

func adjustSchemata(ctx context.Context, adjustment *api.ContainerAdjustment, value string) error {
	schemata := strings.Split(value, ",")
	log.G(ctx).WithField("schemata", schemata).Info("Adjust schemata")
	adjustment.SetLinuxRDTSchemata(schemata)
	return nil
}

func adjustEnableMonitoring(ctx context.Context, adjustment *api.ContainerAdjustment, value string) error {
	enable, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	log.G(ctx).WithField("enablemonitoring", enable).Info("Adjust enablemonitoring")
	adjustment.SetLinuxRDTEnableMonitoring(enable)
	return nil
}
