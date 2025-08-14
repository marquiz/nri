## Sample NRI Request Logger Plugin

This plugin simply logs incoming requests and events. You can configure which
of these the plugin subscribes to. Also, if configured so this plugin can
inject an environment variable or an annotation into containers for testing
and illustrative purposes.

Note that the [differ plugin](../differ) is probably better suited for actual
debugging purposes than this simple logger.

## Deployment

The NRI repository contains kustomize overlays for this plugin at
[contrib/kustomize/logger](../../contrib/kustomize/logger).

Deploy the latest release with:

```bash
kubectl apply -k https://github.com/containerd/nri/contrib/kustomize/logger
```

Deploy the latest development build from tip of the main branch with:

```bash
kubectl apply -k https://github.com/containerd/nri/contrib/kustomize/logger/unstable
```
