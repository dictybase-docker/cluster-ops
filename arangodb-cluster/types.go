package main

import "fmt"

const (
	defaultName         = "arangodb"
	defaultMode         = "Cluster"
	defaultEnvironment  = "Production"
	defaultVersion      = "3.12.10.1"
	defaultArchitecture = "amd64"
	defaultTLSCASecret  = "None"
	arangoDeploymentKey = "arango_deployment"
	nodePoolKey         = "pool"
	nodePoolValue       = "database"
	tolerationKey       = "dedicated"
	tolerationOpEqual   = "Equal"
	tolerationEffect    = "NoSchedule"
	minClusterMembers   = 3
)

// Toleration defines a Kubernetes pod toleration.
type Toleration struct {
	Key      string `json:"key"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
	Effect   string `json:"effect"`
}

// TLSConfig defines TLS configuration for ArangoDeployment.
type TLSConfig struct {
	CASecretName string `json:"caSecretName,omitempty"`
}

// MemberConfig holds configuration for one ArangoDB member group (Agents, DBServers, Coordinators).
type MemberConfig struct {
	Count         int               `json:"count"`
	StorageClass  string            `json:"storageClass,omitempty"`
	StorageSize   string            `json:"storageSize,omitempty"`
	CPURequest    string            `json:"cpuRequest,omitempty"`
	MemoryRequest string            `json:"memoryRequest,omitempty"`
	CPULimit      string            `json:"cpuLimit,omitempty"`
	MemoryLimit   string            `json:"memoryLimit,omitempty"`
	NodeSelector  map[string]string `json:"nodeSelector,omitempty"`
	Tolerations   []Toleration      `json:"tolerations,omitempty"`
}

// SecretConfig holds configuration for the ArangoDB root password secret.
type SecretConfig struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// ArangoClusterConfig holds the complete configuration for the ArangoDB cluster.
type ArangoClusterConfig struct {
	Name         string       `json:"name"`
	Namespace    string       `json:"namespace"`
	Version      string       `json:"version"`
	Environment  string       `json:"environment"`
	Architecture string       `json:"architecture"`
	TLS          TLSConfig    `json:"tls,omitempty"`
	Secret       SecretConfig `json:"secret"`
	Agents       MemberConfig `json:"agents"`
	DBServers    MemberConfig `json:"dbservers"`
	Coordinators MemberConfig `json:"coordinators"`
}

// Validate validates that the cluster configuration meets production requirements.
func (cfg *ArangoClusterConfig) Validate() error {
	if cfg.Namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if cfg.Secret.Name == "" {
		return fmt.Errorf("secret name cannot be empty")
	}
	if cfg.Secret.Password == "" {
		return fmt.Errorf("secret password cannot be empty")
	}
	if cfg.Agents.Count < minClusterMembers {
		return fmt.Errorf("agents count must be at least %d for Cluster mode", minClusterMembers)
	}
	if cfg.DBServers.Count < minClusterMembers {
		return fmt.Errorf("dbservers count must be at least %d for Cluster mode", minClusterMembers)
	}
	if cfg.Coordinators.Count < minClusterMembers {
		return fmt.Errorf("coordinators count must be at least %d for Cluster mode", minClusterMembers)
	}
	if cfg.Agents.StorageSize == "" {
		return fmt.Errorf("agents storageSize is required")
	}
	if cfg.DBServers.StorageSize == "" {
		return fmt.Errorf("dbservers storageSize is required")
	}
	return nil
}
