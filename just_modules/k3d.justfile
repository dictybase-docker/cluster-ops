# Module Variables
cluster_name := "k3d-dev-cluster"
k3s_image := "rancher/k3s:v1.28.8-k3s1"
host_path := env_var_or_default("HOME", "/tmp") + "/data/k3d"
port_forward := "8080:80@loadbalancer"

# Create a single-node k3d cluster with persistent storage mapping.
# Node is labeled kubernetes.io/arch=amd64 to satisfy kube-arangodb's hardcoded
# amd64 node affinity; containerd still pulls the correct arm64 image at runtime.
# Usage: just k3d create-cluster [--name <name>] [--port <p>] [--path <dir>] [--image <img>]
[arg("name", long="name", short="n", help="Cluster name")]
[arg("port", long="port", short="p", help="Port mapping")]
[arg("path", long="path", short="d", help="Host path for persistent storage")]
[arg("image", long="image", short="i", help="k3s image")]
[group('k3d')]
create-cluster name=cluster_name port=port_forward path=host_path image=k3s_image:
    mkdir -p {{ path }}
    k3d cluster create {{ name }} \
      --servers 1 \
      --image {{ image }} \
      --port "{{ port }}" \
      --volume "{{ path }}:/var/lib/rancher/k3s/storage@all" \
      --k3s-node-label "kubernetes.io/arch=amd64@server:0"

# Delete the cluster.
[group('k3d')]
delete-cluster name=cluster_name:
    k3d cluster delete {{ name }}

# Stop the cluster.
[group('k3d')]
stop-cluster name=cluster_name:
    k3d cluster stop {{ name }}

# Start the cluster.
[group('k3d')]
start-cluster name=cluster_name:
    k3d cluster start {{ name }}

# Restart the cluster.
[group('k3d')]
restart-cluster name=cluster_name:
    k3d cluster stop {{ name }}
    k3d cluster start {{ name }}

# List active clusters.
[group('k3d')]
list-clusters:
    k3d cluster list

# Export the kubeconfig to a file.
# Usage: just k3d export-kubeconfig [--name <name>] [--output <path>]
[arg("name", long="name", short="n", help="Cluster name")]
[arg("output", long="output", short="o", help="Output kubeconfig file")]
[group('k3d')]
[no-cd]
export-kubeconfig name=cluster_name output='k3d-dev-cluster.yaml':
    k3d kubeconfig get {{ name }} > {{ output }}
    @echo "Kubeconfig for '{{ name }}' exported to {{ output }}"

# Port-forward the MinIO S3 API (port 9000) to localhost.
# mc alias k3d-minio is pre-configured to use this endpoint.
# Run in a separate terminal; Ctrl-C to stop.
# Usage: just k3d minio-forward [--name <name>] [--namespace <ns>] [--local-port <port>]
[arg("name", long="name", short="n", help="Cluster name")]
[arg("namespace", long="namespace", short="s", help="Kubernetes namespace")]
[arg("local_port", long="local-port", short="p", help="Local port to forward")]
[group('k3d')]
minio-forward name=cluster_name namespace='dev' local_port='9000':
    #!/usr/bin/env bash
    set -euo pipefail
    f=$(mktemp /tmp/k3d-kubeconfig-XXXX.yaml)
    trap 'rm -f "$f"' EXIT
    k3d kubeconfig get {{ name }} > "$f"
    echo "Port-forwarding MinIO → localhost:{{ local_port }} (press Ctrl-C to stop)"
    KUBECONFIG="$f" kubectl port-forward svc/minio {{ local_port }}:9000 -n {{ namespace }}
