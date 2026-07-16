# Production-Grade Kubernetes Cluster Architecture Proposal
## DictyCR Web Platform & Database (GCP Deployment via kOps)

This proposal outlines a production-grade, highly available, and right-sized Kubernetes cluster architecture for the **DictyCR Web Platform & Database** (`https://dictybase.dev/`) using the **kOps** operations tool on **Google Cloud Platform (GCP)**.

---

## 📊 1. Sizing Analysis & Workload Profiling

Your planning baseline is derived from the following annual traffic and utilization aggregates:
*   **Total Users:** 48,592 per year
*   **Total Visits:** 75,796 sessions per year
*   **Processing Load:** 226,690 pageviews per year (involving database processes and analytical operations)

### Average Traffic vs. Peak Traffic Sizing
A simple mathematical average of these annual figures shows:
*   **Average Sessions:** ~208 sessions/day, or ~8.7 sessions/hour.
*   **Average Pageviews:** ~621 pageviews/day, or ~25.8 pageviews/hour.
*   **Average Pageviews/Sec:** ~0.007 pageviews/second.

**CRITICAL WARNING:** Sizing production cloud infrastructure based on annual averages is highly dangerous. Average numbers mask peak demands, seasonal spikes, biological search bursts, and batch jobs. For a public biological database like DictyCR, traffic is inherently bursty. As a hypothetical scenario, the publication of a new scientific paper, classroom lab sessions, or search-engine crawlers (e.g., Googlebot, NCBI indexing) could generate a large burst of concurrent requests in a brief period, which might overwhelm a cluster sized strictly for averages.

### Missing Sizing Metrics
To move this architecture from a starting baseline to a finalized, optimized production state, you must gather or estimate the following metrics:

| Category | Missing Metric | Why It Matters for Sizing |
| :--- | :--- | :--- |
| **Traffic Peaks** | Peak concurrent users (p95/p99) & Peak Requests/Sec (RPS) | Determines the CPU and RAM scheduling capacity needed to avoid HTTP 504 gateway timeouts under peak concurrency. |
| **DB Performance** | Read-to-Write Ratio & Peak Transactions/Sec (TPS) | Determines if you need SSD-based storage vs balanced storage and the necessary IOPS configuration. |
| **Data Size** | Total database size on disk & annual dataset growth | Determines storage volume sizing, backup duration, and backup storage costs. |
| **Working Set** | RAM working set size of the database | Determines the memory family (e.g., high-memory vs standard) to keep indexes in RAM. |
| **Batch/Analysis** | Bioinformatics batch/analytical job concurrency and duration | DictyCR may run heavy sequence alignments or data-processing queries. Sizing depends on whether these run in-cluster as batch pods. |
| **Network Egress** | Average payload size per file transfer/query | Controls network egress billing and determines if a CDN (e.g., Cloud CDN) is required. |

---

## ⚙️ 2. Version & Lifecycle Roadmap (2026 Support Status)

Your current planning baseline relies on **kubectl version 1.28.8** and **kOps version v1.29.2**.

### Recommended Version Strategy
Kubernetes v1.28 and v1.29 are End-of-Life (EOL) upstream. Running EOL Kubernetes in production leaves you exposed to unpatched security vulnerabilities and deprives you of modern cloud-provider driver compatibility. (Source: [Kubernetes Supported Versions](https://kubernetes.io/releases/version-skew-policy/)).

As of **2026**, kOps has reached stable releases such as **v1.35.x** (with v1.36 in active testing). We recommend planning your deployment with a stable, fully supported combination:
*   **Target Release:** **Kubernetes v1.35.x (or v1.34.x as a conservative target) managed by matching kOps v1.35.x/v1.34.x**.
*   **Kubectl Alignment:** Align your local administrative tool directly to the target cluster version (e.g., **kubectl v1.35.x**), adhering to the Kubernetes version skew policy which only supports a one-minor-version discrepancy.

```
[Local CLI]                     [GCS State Store]                 [GCP Compute Engine]
kubectl v1.35.x  ─────────►   kOps v1.35.x Configuration  ───►   Kubernetes Cluster v1.35.x
```

### Upgrading an Existing v1.28/v1.29 Cluster
If you are upgrading an existing production deployment rather than starting fresh:
1.  **Sequential Upgrades Only:** You cannot skip minor versions. You must execute minor-by-minor sequential upgrades (1.28 -> 1.29 -> 1.30 -> 1.31 -> 1.32 -> 1.33 -> 1.34 -> 1.35).
2.  **API Deprecation Audit:** Audit your deployment manifests and Helm charts at each step to replace APIs deprecated or removed in newer releases.
3.  **Unified Reconciliation (kOps 1.31+):** Note that starting with kOps 1.31, cluster updates for Kubernetes 1.31 or newer must utilize the unified command:
    ```bash
    kops reconcile cluster
    ```
    This replaces the legacy `kops update cluster --yes` and `kops rolling-update cluster --yes` sequence (Source: [kOps Upgrades Documentation](https://kops.sigs.k8s.io/tutorial/upgrading-kubernetes/)).
4.  **Staging Validation:** Always execute and validate the complete upgrade sequence in an identical staging environment first.

---

## 🖥️ 3. Control Plane Sizing & Storage

For a production-grade GCP deployment, a highly available, multi-zonal control plane is required to maintain cluster control and `etcd` consensus.

```
                  ┌───────────────────────────────┐
                  │       Compute Region          │
                  │                               │
                  │  Zone A     Zone B     Zone C │
                  │  ┌─────┐    ┌─────┐    ┌─────┐│
                  │  │ CP1 │    │ CP2 │    │ CP3 ││
                  │  └─────┘    └─────┘    └─────┘│
                  │   etcd1      etcd2      etcd3 │
                  └──────▲────────▲──────────▲────┘
                         │        │          │
                         └────────┴──────────┘ (Raft Quorum)
```

### Control Plane Node Sizing Recommendation
*   **Node Count:** **3 control-plane nodes** distributed across three separate GCP zones (e.g., `us-central1-a`, `us-central1-b`, `us-central1-c`). This is required to maintain `etcd` Raft quorum. A 1-node control plane has a single point of failure; a 2-node control plane cannot survive a 1-node loss because quorum (`(N/2)+1 = 2`) is instantly lost.
*   **Machine Type:**
    *   **`e2-standard-2`** (2 vCPUs, 8 GB RAM) is a cost-oriented starting point. While the E2 family is sufficient for modest API loads, workloads must be monitored to ensure sufficient compute resources are available.
    *   **`n2-standard-2`** (2 vCPUs, 8 GB RAM) is a recommended alternative that may offer more predictable performance, though workloads should be benchmarked to confirm latency improvements.
*   **Boot Disk:** **64 GB `pd-balanced`** per node. This provides solid-state-like boot performance at a lower price point than `pd-ssd`, with ample space for OS packages, container runtimes, and local logs.

### etcd Storage Configuration (Critical for Stability)
`etcd` is extremely sensitive to disk write/fsync latency. Slow disk performance causes etcd leader-election timeouts, cascading into API server drops and cluster failure.
1.  **Volume Isolation:** etcd-main and etcd-events must reside on **dedicated GCP Persistent Disks**, separate from the OS boot disk.
2.  **Disk Type:** **`pd-ssd`** (SSD Persistent Disk) is the recommended initial choice for etcd storage. While `pd-balanced` is not categorically invalid if measured fsync latency consistently meets etcd requirements, `pd-ssd` offers lower latency and higher baseline performance.
3.  **Capacity and Sizing:** Treat **20 GB to 50 GB per etcd volume** as an initial baseline. Because GCP Persistent Disk performance (IOPS and throughput limits) scales linearly with provisioned capacity, extremely small disks (e.g., 10 GB) may be severely throttled by GCP. Sizing must be selected to satisfy etcd's target write/fsync latencies under simulated peak loads.

---

## 👥 4. Worker Node Capacity, Disk Sizing & Elasticity

To support DictyCR's dual-nature workload—serving general web/API traffic and processing database-heavy, memory-intensive analytical operations—we recommend a specialized node-pool layout.

### Recommended Node Pool Architecture (Highly Available Plan)

```
                            [ Custom GCP VPC ]
                                    │
                  ┌─────────────────┴─────────────────┐
                  ▼                                   ▼
      ┌────────────────────────┐          ┌────────────────────────┐
      │  Stateless Node Pool   │          │  Stateful/Database Pool│
      │   (Web frontend, API)  │          │   (Primary/Replicas)   │
      ├────────────────────────┤          ├────────────────────────┤
      │ 3-6x e2-standard-4     │          │ 3x n2-standard-4       │
      │ Elastic (Autoscaling)  │          │ Static (Non-elastic)   │
      └────────────────────────┘          └────────────────────────┘
```

#### 1. Stateless Web/API Node Pool (Elastic)
*   **Instance Family:** **`e2-standard-4`** (4 vCPUs, 16 GB RAM). This is highly cost-effective and perfectly balances memory and CPU for standard web/API runtimes.
*   **Elasticity:** **Elastic**. Spanned across 3 zones using GCE Managed Instance Groups (MIGs) with kOps-integrated Cluster Autoscaler. Sized from **3 nodes minimum to 6 nodes maximum**. Three nodes ensure that if an entire zone experiences a temporary outage, the remaining two zones have the capacity to keep the web traffic reachable.
*   **Boot Disk:** **100 GB `pd-balanced`**.

#### 2. Stateful/Database Node Pool (Static)
*   **Instance Family:** **`n2-standard-4`** (4 vCPUs, 16 GB RAM) or **`n2-highmem-4`** (4 vCPUs, 32 GB RAM) depending on working set requirements. The `N2` series is designed for more predictable performance, which can be critical for stable database transaction processing.
*   **Elasticity:** **Static**. Keep this pool static (e.g., matching your database replication topology). Elastic autoscaling is highly discouraged for stateful relational databases because rapid scale-down events can interrupt active TCP connections, disrupt write operations, and cause replica synchronization issues.
*   **Boot Disk:** **100 GB `pd-balanced`**.

#### 3. Analytical/Batch Node Pool (Optional, Cost-Optimized)
*   **Instance Family:** **`e2-standard-4`** running on **Spot VMs** (formerly Preemptible VMs).
*   **Elasticity:** **Elastic (0 to 4 nodes)**. Sized down to 0 when no analytical jobs are active to eliminate idle compute costs.
*   **Rationale:** GCP Spot VMs offer up to a **90% discount** compared to standard instances (Source: [GCP Spot VM Docs](https://cloud.google.com/compute/docs/instances/spot)). If biological sequence alignment or analytical operations are configured as batch jobs, they can run on Spot instances. Note that while GCE gives a 30-second graceful shutdown notice, you should not assume that every batch job can checkpoint within this window; jobs should be stateless, idempotent, or robust to interruption. This pool must be isolated using Kubernetes **Node Taints** and **Tolerations** to ensure regular web pods do not run on Spot instances.

---

## 📈 5. Cluster & Pod Scaling Mechanisms

To handle unexpected traffic spikes without manual intervention, you must enable three distinct tiers of autoscaling:

```
┌────────────────────────────────────────────────────────┐
│ 1. Pod Replica Scaling (HPA)                           │
│    Web Pods scale horizontally                         │
└───────────────────────────┬────────────────────────────┘
                            │ (Resource Requests)
                            ▼
┌────────────────────────────────────────────────────────┐
│ 2. Node VM Scaling (Cluster Autoscaler)                │
│    MIGs add GCE instances to the cluster               │
└────────────────────────────────────────────────────────┘
```

1.  **Horizontal Pod Autoscaler (HPA):**
    *   **Scope:** Applied to stateless deployments (frontend and API).
    *   **Configuration:** Target average CPU utilization at **65%** (treat this as an initial hypothesis that must be validated with simulated load testing under peak traffic).
    *   **Prerequisites:** Requires the **Metrics Server** addon enabled in kOps.
2.  **Vertical Pod Autoscaler (VPA):**
    *   **Scope:** Applied to stateful or single-replica background workloads (like the primary database or heavy queue consumers) that cannot scale horizontally.
    *   **Configuration:** Run VPA in **"Recommendation Only" (Off)** mode. It observes real-world memory and CPU consumption and provides suggestions on what resource requests and limits should be defined in your Helm charts, preventing out-of-memory (OOM) crashes.
3.  **Cluster Autoscaler (CA):**
    *   **Scope:** Triggers automatically when a pod cannot be scheduled due to insufficient CPU/memory on existing nodes (scaling is driven by pod resource requests, not raw CPU utilization thresholds). It communicates directly with GCE MIGs to provision new VMs.
    *   **Scale-down Tuning:** Setting conservative scale-down parameters (such as node under-utilization dropping below 50% for 10 minutes) can be evaluated during testing to avoid "thrashing" (constant adding/removing of nodes).

---

## 💾 6. Database Storage & Retrieval

Since DictyCR is a database-backed biological platform, data persistence, integrity, and retrieval speed are your highest priorities.

### Option A: Fully Managed Database (Strongly Recommended)
We strongly recommend hosting your database on **Google Cloud SQL (PostgreSQL or MySQL)** instead of self-hosting it inside the Kubernetes cluster.
*   **Why:** Cloud SQL handles automatic regional multi-zone replication, automated point-in-time recovery (PITR) backups, security patching, and transparent storage autoscaling out-of-the-box. This drastically reduces administrative overhead for small engineering teams.
*   **Compatibility Warning:** Only pursue this option if DictyCR's engine, custom biological database extensions, and operational tooling are fully compatible with standard Google Cloud SQL features.
*   **Disk Sizing:** Sized initially to your dataset + 1 year of expected growth, utilizing high-performance storage.

### Option B: Self-Hosted In-Cluster Database
If you must host the database inside the kOps cluster, configure your storage as follows:
*   **Database HA Topology:** Do not describe a consolidated two-node pool as production database HA. To achieve true database high availability on Kubernetes, you must deploy a supported database operator (such as **CloudNativePG** or **PGO**) managing a replication topology supported by that operator (e.g., three failure-domain-aware database instances if the selected engine/operator uses quorum, or a primary-secondary setup with a monitoring sentinel). Each replica must have its own independent Persistent Volume (replicated at the database engine level, not shared storage).
*   **Storage Layer:** Rather than prescribing Regional PD for every database replica, treat Regional PD as an optional storage-level replication choice with cost and failover tradeoffs. A database operator commonly uses one zonal volume per replica and engine-level replication; Regional PD provides synchronous storage replication across zones but **does not perform database failover** at the application level.
*   **Kubernetes Constraints:**
    *   Use **Pod Anti-Affinity rules** to ensure database replicas are physically scheduled on different nodes in different zones.
    *   Use a **PodDisruptionBudget (PDB)** with `minAvailable: 1` to prevent the Cluster Autoscaler or administrative operations from voluntarily draining too many database nodes concurrently. Note that PDBs protect against voluntary disruptions but do not protect against underlying VM crashes.
    *   Implement scheduled backups with Point-In-Time Recovery (PITR) and perform regular restore drills to prove RPO/RTO objectives are realistic.

---

## 🔒 7. GCP kOps-Specific Quirks, Networking & Security

Deploying a kOps cluster on GCP has unique architecture details and gotchas that differ significantly from GKE or AWS kOps.

### GCS State Store Bucket Hardening
The kOps state store holds your entire cluster configuration and security credentials. It must be locked down:
*   **Uniform Bucket-Level Access (UBLA):** Enforce consistent IAM permissions across all objects.
*   **Public Access Prevention (PAP):** Block public access.
*   **Object Versioning:** Enable versioning (`gsutil versioning set on gs://...`) to protect against accidental state corruption or deletion.
*   **Retention Policies:** Set Object Lifecycle Management rules to clean up or archive extremely old state versions.
*   **Encryption:** Enable encryption at rest, utilizing Customer-Managed Encryption Keys (CMEK) if dictated by compliance.

### Networking & Custom VPC Best Practices
*   **Avoid Auto-Mode VPC:** Always provision your kOps cluster in a **Custom Mode VPC** to prevent CIDR collisions and keep IP subnets segregated.
*   **Topology Security (Private):** Enforce a **Private Topology** in your kOps manifest:
    ```yaml
    spec:
      topology:
        masters: private
        nodes: private
    ```
    This ensures both control plane and worker nodes are provisioned with private-only GCP IP addresses (preventing direct internet-based attacks).
*   **Internet Egress:** Provision a **Google Cloud NAT** gateway in your VPC to allow private nodes to fetch container images and run OS security updates securely without being exposed to incoming internet traffic.
*   **CNI Selection:** Configure **Cilium** as the recommended CNI for advanced security policies, and refer to standard kOps GCE networking options without assuming specific latency performance benefits.
*   **API Load Balancer Security:** Restrict public API load balancer access using the `kubernetesApiAccess` block to specific authorized administrative CIDRs, rather than leaving it open to the entire internet (`0.0.0.0/0`). Leaving the cluster API wide open is a severe security risk. Do not present unrestricted public topology as an acceptable production cost option.

### Decoupling Administrative and Node Workload Identities
Administrative and node workload identities must be separated, and permissions derived from the selected kOps release's documented requirements.
*   **Administrative Identity:** The user or CI/CD runner executing `kops` commands requires read/write IAM permissions on the GCS state bucket.
*   **Node Workload Identity:** Worker and control-plane nodes run under restricted, least-privileged Google Cloud Service Accounts assigned to their InstanceGroups. The node service accounts should have the minimum bucket permissions documented for the selected kOps release.

### OS Image Warning (The Cloud-Init Gotcha)
*   *GCE Support Status:* Google Cloud Platform (GCP) is officially supported in kOps alongside AWS. (Source: [kOps Supported Providers](https://kops.sigs.k8s.io/)). The legacy feature flag `KOPS_FEATURE_FLAGS=AlphaAllowGCE` is obsolete and no longer required in modern stable versions of kOps (such as v1.35.x).
*   *Gotcha:* Do not use standard GCP Red Hat Enterprise Linux (RHEL) or Rocky Linux images for your cluster nodes. Historically, default GCP RHEL/Rocky images do not have `cloud-init` installed by default. Because kOps relies on `cloud-init` to boot `nodeup` (the engine that configures Kubernetes components on VM boot), your nodes will boot successfully on GCP but will hang forever and fail to register with the cluster.
*   *Remediation:* Use Google **Container-Optimized OS (COS)** or **Ubuntu Server**, which have full, native, out-of-the-box support in kOps.

---

## ⚖️ 8. High-Availability vs. Cost-Optimized Comparison

Production cloud costs depend heavily on regional variables, exact database configurations, NAT gateway egress volume, Persistent Disk capacity, and network egress traffic. You must model your actual workloads using the official [GCP Pricing Calculator](https://cloud.google.com/products/calculator) instead of relying on speculative estimates. Both of the designs below utilize private nodes, Cloud NAT, and restricted API access to ensure security:

| Architectural Component | Highly Available Plan (Recommended) | Cost-Optimized Plan |
| :--- | :--- | :--- |
| **Control Plane Nodes** | **3 × `e2-standard-2`** (Distributed across 3 separate zones) | **3 × `e2-standard-2`** (Do not compromise control-plane HA in production!) |
| **Worker Nodes** | **3-6 × `e2-standard-4`** (Stateless) + **3 × `n2-standard-4`** (Dedicated DB) + **0-4 Spot Nodes** (Batch) | **2-4 × `e2-standard-2`** (Consolidated Node Pool for Web and DB) |
| **Database Tier** | **Managed Cloud SQL (PostgreSQL/MySQL)** with multi-zone HA | **Self-hosted non-HA Database (Single Replica)** using zonal `pd-balanced` storage |
| **Network Security** | Custom VPC, Private Topology, Cloud NAT, restricted API CIDR | Custom VPC, Private Topology, Cloud NAT, restricted API CIDR |
| **Zone Failure Tolerance** | **High Tolerance**. Redundancies in compute and database minimize the risk of downtime, though zonal failures may still cause brief pod/database failover and transient disruption. | **Low-to-Moderate Tolerance**. Web app stays up, but database is non-HA and will experience downtime during a node or zonal outage, requiring manual restore or recovery. |

---

## 🛠️ 9. Illustrative kOps Deployment Specification

Deploying a highly available kOps cluster on GCP involves generating a cluster template and then editing it to match your architecture.

### Step 1: Initialize Environment Variables
```bash
export PROJECT="your-gcp-project-id"
export KOPS_STATE_STORE="gs://dictycr-kops-state-store"
export NAME="dictycr-cluster.k8s.local"
```

### Step 2: Create Cluster Configuration Template
This command generates the cluster template in your GCS bucket without creating actual resources. Note that modern kOps uses `--control-plane-count` and `--control-plane-zones` (rather than deprecated `master` terminology) and configures explicit boot disk sizes:
```bash
kops create cluster \
    --name=${NAME} \
    --state=${KOPS_STATE_STORE} \
    --project=${PROJECT} \
    --zones=us-central1-a,us-central1-b,us-central1-c \
    --control-plane-zones=us-central1-a,us-central1-b,us-central1-c \
    --node-count=3 \
    --node-size=e2-standard-4 \
    --control-plane-count=3 \
    --control-plane-size=e2-standard-2 \
    --control-plane-volume-size=64 \
    --node-volume-size=100 \
    --topology=private \
    --networking=cilium \
    --cloud=gce \
    --kubernetes-version=${KUBERNETES_VERSION}
```

### Step 3: Edit Cluster Configuration
After running the creation template, execute:
```bash
kops edit cluster --name=${NAME} --state=${KOPS_STATE_STORE}
```
Modify or add the following properties inside the manifest to configure etcd and security parameters:

| Manifest Section | Field Path | Recommended Value |
| :--- | :--- | :--- |
| **GCP Project** | `spec.cloudProvider.gce.project` | `"your-gcp-project-id"` |
| **Service Account** | `spec.cloudProvider.gce.serviceAccount` | `"kops-nodes-sa@your-gcp-project-id.iam.gserviceaccount.com"` |
| **API Access** | `spec.kubernetesApiAccess` | `["your-administrative-cidr/32"]` (Restricted access) |
| **CSI Driver** | `spec.cloudProvider.gce.pdCSIDriver.enabled` | `true` |
| **etcd Volumes (main)** | `spec.etcdClusters[0].etcdMembers[*].volumeType` | `pd-ssd` (Dedicated SSD storage) |
| **etcd Volumes (events)**| `spec.etcdClusters[1].etcdMembers[*].volumeType` | `pd-ssd` (Dedicated SSD storage) |

### Step 4: Provision Infrastructure (kOps v1.31+)
To build and reconcile the GCE instances, disks, load balancers, and network routes:
```bash
kops reconcile cluster --name=${NAME} --state=${KOPS_STATE_STORE} --yes
```

To verify the cluster health after creation:
```bash
kops validate cluster --name=${NAME} --state=${KOPS_STATE_STORE} --wait 10m
```
