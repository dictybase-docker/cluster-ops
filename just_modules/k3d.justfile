# Create a single-node k3d cluster with persistent storage mapping
# This maps the host path to /var/lib/rancher/k3s/storage@all for dynamic provisioning
# Default Kubernetes version is 1.28.8
[group('k3d')]
create-cluster name='my-dev-cluster' port='8080:80@loadbalancer' host_path="$HOME/k3d-storage" image='rancher/k3s:v1.28.8-k3s1':
    mkdir -p {{ host_path }}
    k3d cluster create {{ name }} \
      --servers 1 \
      --image {{ image }} \
      --port "{{ port }}" \
      --volume "{{ host_path }}:/var/lib/rancher/k3s/storage@all"

# Delete a k3d cluster
[group('k3d')]
delete-cluster name='my-dev-cluster':
    k3d cluster delete {{ name }}

# List k3d clusters
[group('k3d')]
list-clusters:
    k3d cluster list

# Export the kubeconfig for a specific cluster
[group('k3d')]
export-kubeconfig name='my-dev-cluster' output='kubeconfig.yaml':
    k3d kubeconfig get {{ name }} > {{ output }}
    @echo "Kubeconfig exported to {{ output }}"

