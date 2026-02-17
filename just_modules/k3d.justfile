# Create a single-node k3d cluster with persistent storage mapping
# This maps the host path to /var/lib/rancher/k3s/storage@all for dynamic provisioning
[group('k3d')]
create-cluster name='my-dev-cluster' port='8080:80@loadbalancer' host_path="$HOME/k3d-storage":
    mkdir -p {{ host_path }}
    k3d cluster create {{ name }} \
      --servers 1 \
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

