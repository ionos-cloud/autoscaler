package iec

import (
	"net/http"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
)

//go:generate mockery -name Client -case snake -dir . -output ./cloudprovider/iec/mocks/client
type Client interface {

	// GetNodePool gets a single node pool from the iec API
	GetKubernetesNodePool(clusterID, nodepoolID string) (*profitbricks.KubernetesNodePool, error)

	// ListNodePools lists all the node pools in a kubernetes cluster.
	ListKubernetesNodePools(clusterID string) (*profitbricks.KubernetesNodePools, error)

	// UpdateNodePool updates a specific node pool in a kubernetes cluster.
	UpdateKubernetesNodePool(clusterID, nodepoolID string, nodepool profitbricks.KubernetesNodePool) (*profitbricks.KubernetesNodePool, error)

	// GetNode gets a single node for given clusterID, nodepoolID, nodeID
	GetKubernetesNode(clusterID, nodepoolID, nodeID string) (*profitbricks.KubernetesNode, error)

	// GetNodes gets all nodes for given clusterID, nodepoolID
	ListKubernetesNodes(clusterID, nodepoolID string) (*profitbricks.KubernetesNodes, error)

	// DeleteNode deletes a node by clusterID, nodepoolID, nodeID
	DeleteKubernetesNode(clusterID, nodepoolID, nodeID string) (*http.Header, error)
}
