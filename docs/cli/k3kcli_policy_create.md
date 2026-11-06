## k3kcli policy create

Create a new policy.

```
k3kcli policy create [flags]
```

### Examples

```
k3kcli policy create [command options] NAME
```

### Options

```
      --annotations stringArray   Annotations to add to the policy object (e.g. key=value)
      --audit                     enforce the audit log of the K3s servers of the clusters bound to the policy
      --audit-policy string       path to an audit.k8s.io/v1 Policy file for the audit log (implies --audit)
  -h, --help                      help for create
      --labels stringArray        Labels to add to the policy object (e.g. key=value)
      --mode string               The allowed mode type of the policy (default "shared")
      --namespace strings         The namespaces where to bind the policy
      --overwrite                 Overwrite namespace binding of existing policy
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli policy](k3kcli_policy.md)	 - K3k policy command.

