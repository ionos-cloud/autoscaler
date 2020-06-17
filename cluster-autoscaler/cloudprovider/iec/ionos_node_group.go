package iec

import (
	"errors"
	"fmt"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
)

type NodeGroup struct {
	id         string
	clusterID  string
	clientConf *clientConfObj
	nodePool   *profitbricks.KubernetesNodePool

	minSize int
	maxSize int
}

// Maximum size of the node group.
func (n *NodeGroup) MaxSize() int {
	return n.maxSize
}

// Minimum size of the node group.
func (n *NodeGroup) MinSize() int {
	return n.minSize
}

// Target size of the node group. Might be different to the actual size, but
// should stabilize.
func (n *NodeGroup) TargetSize() (int, error) {
	return int(n.nodePool.Properties.NodeCount), nil
}

// Increases node group size
func (n *NodeGroup) IncreaseSize(delta int) error {
	klog.V(3).Infof("Increasing nodegroup %s size, current: %d, delta: %d", n.id, n.nodePool.Properties.NodeCount, delta)
	if delta <= 0 {
		return fmt.Errorf("delta must be positive, have: %d", delta)
	}

	targetSize := n.nodePool.Properties.NodeCount + uint32(delta)

	if targetSize > uint32(n.MaxSize()) {
		return fmt.Errorf("size increase is too large. current: %d, desired: %d, max: %d",
			n.nodePool.Properties.NodeCount, targetSize, n.MaxSize())
	}

	upgradeInput := profitbricks.KubernetesNodePool{
		Properties: &profitbricks.KubernetesNodePoolProperties{
			NodeCount: targetSize,
		},
	}

	iecConfig, err := n.clientConf.getIECConfig(n.nodePool.Properties.DatacenterID)
	if err != nil {
		return fmt.Errorf("error getting IECClient config: %v", err)
	}
	var iecClient *iecClient
	var allInvalid bool
	for i, t := range iecConfig.Tokens {
		iecClient = iecClientGetter(t, iecConfig.Endpoint, iecConfig.Insecure)
		_, err = iecClient.UpdateKubernetesNodePool(n.clusterID, n.nodePool.ID, upgradeInput)
		if err != nil {
			if profitbricks.IsStatusUnauthorized(err) {
				klog.V(5).Infof("Token %d invalid, trying next one", i)
				allInvalid = true
				continue
			}
			return fmt.Errorf("failed to update nodepool: %v", err)
		}
		allInvalid = false
		break
	}
	if allInvalid {
		return errors.New("All tokens invalid")
	}

	klog.V(3).Infof("Waiting for %v nodepool to reach target size. Polling every %v.", n.clientConf.iecPollTimeout, n.clientConf.iecPollInterval)
	err = wait.PollImmediate(
		n.clientConf.iecPollInterval,
		n.clientConf.iecPollTimeout,
		iecClient.PollNodePoolNodeCount(n.clusterID, n.nodePool.ID, targetSize))
	if err != nil {
		return fmt.Errorf("failed to wait for nodepool update: %v", err)
	}

	n.nodePool, err = iecClient.GetKubernetesNodePool(n.clusterID, n.nodePool.ID)
	if err != nil {
		return fmt.Errorf("failed to get nodepool: %v", err)
	}
	return nil
}

// DeleteNodes deletes nodes from this node group (and also decreasing the size
// of the node group with that). Error is returned either on failure or if the
// given node doesn't belong to this node group. This function should wait
// until node group size is updated. Implementation required.
func (n *NodeGroup) DeleteNodes(kubernetesNodes []*apiv1.Node) error {
	klog.V(3).Infof("Deleting %d nodes", len(kubernetesNodes))
	for _, node := range kubernetesNodes {
		klog.V(3).Infof("Deleting node %s with id %s", node.Name, node.Spec.ProviderID)
		// Use node.Spec.ProviderID as to retrieve nodeID
		nodeID := toNodeID(node.Spec.ProviderID)
		datacenterID := n.nodePool.Properties.DatacenterID
		iecConfig, err := n.clientConf.getIECConfig(datacenterID)
		if err != nil {
			return fmt.Errorf("error getting IECClient config: %v", err)
		}
		var iecClient *iecClient
		var allInvalid bool
		for i, t := range iecConfig.Tokens {
			iecClient = iecClientGetter(t, iecConfig.Endpoint, iecConfig.Insecure)
			_, err := iecClient.DeleteKubernetesNode(n.clusterID, n.id, nodeID)
			if err != nil {
				if profitbricks.IsStatusNotFound(err) {
					allInvalid = false
					break
				}
				if profitbricks.IsStatusUnauthorized(err) {
					klog.V(5).Infof("Token %d invalid, trying next one", i)
					allInvalid = true
					continue
				}
				return fmt.Errorf("failed to delete node %s from cluster %s: %v", n.id, n.clusterID, err)
			}
			allInvalid = false
			break
		}
		if allInvalid {
			return errors.New("All tokens invalid")
		}

		targetSize := n.nodePool.Properties.NodeCount - 1
		err = wait.PollImmediate(
			n.clientConf.iecPollInterval,
			n.clientConf.iecPollTimeout,
			iecClient.PollNodePoolNodeCount(n.clusterID, n.nodePool.ID, targetSize))
		if err != nil {
			return fmt.Errorf("failed to wait for nodepool update: %v", err)
		}
		n.nodePool.Properties.NodeCount = targetSize
	}
	return nil
}

// DecreaseTargetSize decreases the target size of the node group. This function
// doesn't permit to delete any existing node and can be used only to reduce the
// request for new nodes that have not been yet fulfilled. Delta should be negative.
// It is assumed that cloud provider will not delete the existing nodes when there
// is an option to just decrease the target. Implementation required.
func (n *NodeGroup) DecreaseTargetSize(delta int) error {
	if delta >= 0 {
		return fmt.Errorf("delta must be negative, have: %d", delta)
	}

	targetSize := int(n.nodePool.Properties.NodeCount) + delta
	if targetSize < 0 || targetSize < n.MinSize() {
		return fmt.Errorf("size decrease is too small. current: %d, desired: %d, min: %d",
			n.nodePool.Properties.NodeCount, targetSize, n.MinSize())
	}

	// Since Ionos CloudAPI does not support the requested behavior we just always return an error.
	// update internal cache
	return errors.New("Currently not supported behavior")
}

// Id returns an unique identifier of the node group.
func (n *NodeGroup) Id() string {
	return fmt.Sprint(n.id)
}

// Debug returns a string containing all information regarding this node group.
func (n *NodeGroup) Debug() string {
	return fmt.Sprintf("cluster ID: %s, nodegroup ID: %s (min: %d, max: %d)", n.clusterID, n.Id(), n.MinSize(), n.MaxSize())
}

// Nodes returns a list of all nodes that belong to this node group.  It is
// required that Instance objects returned by this method have Id field set.
// Other fields are optional.
func (n *NodeGroup) Nodes() ([]cloudprovider.Instance, error) {
	if n.nodePool != nil {
		klog.V(5).Infof("Getting nodes for nodegroup: %s backed by %s", n.id, n.nodePool.ID)
	}
	if n.nodePool == nil {
		klog.Errorf("No nodepool associated with nodegroup: %s", n.id)
		return nil, errors.New("node pool instance is not created")
	}
	datacenterID := n.nodePool.Properties.DatacenterID
	iecConfig, err := n.clientConf.getIECConfig(datacenterID)
	if err != nil {
		return nil, fmt.Errorf("error getting IECClient config: %v", err)
	}
	var nodes *profitbricks.KubernetesNodes
	var iecClient *iecClient
	var allInvalid bool
	for i, t := range iecConfig.Tokens {
		iecClient = iecClientGetter(t, iecConfig.Endpoint, iecConfig.Insecure)
		nodes, err = iecClient.ListKubernetesNodes(n.clusterID, n.nodePool.ID)
		if err != nil {
			if profitbricks.IsStatusUnauthorized(err) {
				klog.V(5).Infof("Token %d invalid, trying next one", i)
				allInvalid = true
				continue
			}
			return nil, fmt.Errorf("failed to get nodes for nodepool %s: %v", n.nodePool.ID, err)
		}
		allInvalid = false
		break
	}
	if allInvalid {
		return nil, errors.New("All tokens invalid")
	}

	klog.V(5).Infof("Nodes: %+v", nodes.Items)
	instances := toInstances(nodes)
	klog.V(5).Infof("Nodes: %+v", nodes)
	return instances, nil
}

// TemplateNodeInfo returns a schedulerframework.NodeInfo structure of an empty
// (as if just started) node. This will be used in scale-up simulations to
// predict what would a new node look like if a node group was expanded. The
// returned NodeInfo is expected to have a fully populated Node object, with
// all of the labels, capacity and allocatable information as well as all pods
// that are started on the node by default, using manifest (most likely only
// kube-proxy). Implementation optional.
func (n *NodeGroup) TemplateNodeInfo() (*schedulerframework.NodeInfo, error) {
	return nil, cloudprovider.ErrNotImplemented
}

// Exist checks if the node group really exists on the cloud provider side.
// Allows to tell the theoretical node group from the real one. Implementation
// required.
func (n *NodeGroup) Exist() bool {
	return n.nodePool != nil
}

// Create creates the node group on the cloud provider side. Implementation
// optional. Not implemented for iec enterprise cloud.
func (n *NodeGroup) Create() (cloudprovider.NodeGroup, error) {
	return nil, cloudprovider.ErrNotImplemented
}

// Delete deletes the node group on the cloud provider side.  This will be
// executed only for autoprovisioned node groups, once their size drops to 0.
// Implementation optional. Not implemented for iec enterprise cloud.
func (n *NodeGroup) Delete() error {
	return cloudprovider.ErrNotImplemented
}

// Autoprovisioned returns true if the node group is autoprovisioned. An
// autoprovisioned group was created by CA and can be deleted when scaled to 0.
// Atuoprovisioned groups are curretly not supported for iec enterprise cloud.
func (n *NodeGroup) Autoprovisioned() bool {
	return false
}
