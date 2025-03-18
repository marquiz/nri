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
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	k8sclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func (p *plugin) startConfigMapWatcher(ctx context.Context, kubeconfig string) error {
	// Watch for changes
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	cli, err := getKubernetesClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client: %v", err)
	}

	watcher, err := cli.CoreV1().ConfigMaps(namespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", configmap),
	})
	if err != nil {
		return fmt.Errorf("failed to watch ConfigMap: %v", err)
	}

	p.l.Infof("Watching ConfigMap %s/%s", namespace, configmap)
	go func() {
		for event := range watcher.ResultChan() {
			switch event.Type {
			case watch.Added, watch.Modified:
				configMap, ok := event.Object.(*corev1.ConfigMap)
				if !ok {
					p.l.Errorf("Unexpected object type %T", event.Object)
					continue
				}
				p.l.Info("ConfigMap updated")

				data, exists := configMap.Data["resources"]
				if !exists {
					p.l.Info("No 'resources' key found in ConfigMap, using default resource config")
					continue
				}
				p.updateNodeResourcesRequestFromConfig([]byte(data))
			case watch.Deleted:
				p.l.Info("ConfigMap deleted")
			}
		}
	}()
	return nil
}

func getKubernetesClient(kubeconfig string) (k8sclient.Interface, error) {
	cfg, err := getKubernetesClientConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	return k8sclient.NewForConfig(cfg)
}

func getKubernetesClientConfig(kubeconfig string) (*restclient.Config, error) {
	if kubeconfig == "" {
		return restclient.InClusterConfig()
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}
