# Module Variables
cluster_name := "k3d-dev-cluster"
k3s_image    := "rancher/k3s:v1.28.8-k3s1"
host_path    := env_var_or_default("HOME", "/tmp") + "/data/k3d"
port_forward := "8080:80@loadbalancer"

# Create a single-node k3d cluster with persistent storage mapping
[group('k3d')]
create-cluster name=cluster_name port=port_forward path=host_path image=k3s_image:
    mkdir -p {{ path }}
    k3d cluster create {{ name }} \
      --servers 1 \
      --image {{ image }} \
      --port "{{ port }}" \
      --volume "{{ path }}:/var/lib/rancher/k3s/storage@all"

# Delete the cluster
[group('k3d')]
delete-cluster name=cluster_name:
    k3d cluster delete {{ name }}

# List active clusters
[group('k3d')]
list-clusters:
    k3d cluster list

# Export the kubeconfig to a file
[group('k3d')]
export-kubeconfig name=cluster_name output='kubeconfig.yaml':
    k3d kubeconfig get {{ name }} > {{ output }}
    @echo "Kubeconfig for '{{ name }}' exported to {{ output }}"
