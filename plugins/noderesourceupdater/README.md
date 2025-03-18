## Demo plugin for dynamic node resources

This plugin is a demonstration of of how node capacity (resources)
can be dynamically updated from an NRI plugin.

### Usage

Instructions how to use the plugin in a local one-node Kubernetes
cluster.

1. CRI-O

    ```bash
    git clone https://github.com/marquiz/cri-o -b devel/resource-discovery-2-nri
    cd cri-o
    make
    sudo ./bin/crio
    ```

2. Kubernetes

    ```bash
    git clone https://github.com/marquiz/kubernetes -b devel/resource-discovery-2-hot-plug
    cd kubernetes
    make
    export CONTAINER_RUNTIME_ENDPOINT=unix:///run/crio/crio.sock
    export FEATURE_GATES=KubeletCRIResourceDiscovery=true,NodeResourceHotPlug=true
    ./hack/local-up-cluster.sh -O
    ```

3. Build the NRI plugin

    ```bash
    git clone https://github.com/marquiz/nri -b devel/resource-discovery
    cd nri/plugins/noderesourceupdater
    go build -v .
    ```

4. Manage node resources via a config file

    Start the NRI plugin:

    ```bash
    sudo ./noderesourceupdater -config config.yaml
    ```

    Edit node resources:

    ```bash
    vim config.yaml
    ```

5. Manage node resources via a ConfigMap

    Alternatively, you can manage node resources via a ConfigMap.

    Start the NRI plugin:

    ```bash
    sudo ./noderesourceupdater -kubeconfig /var/run/kubernetes/admin.kubeconfig -configmap resource-topology
    ```

    Create the ConfigMap:

    ```bash
    export KUBECONFIG=/var/run/kubernetes/admin.kubeconfig
    kubectl apply -f configmap.yaml
    ```

    Edit node resources:

    ```bash
    kubectl edit configmap resource-topology
    ```
