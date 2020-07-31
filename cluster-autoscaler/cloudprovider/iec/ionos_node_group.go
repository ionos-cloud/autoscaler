package iec

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/profitbricks/profitbricks-sdk-go/v5"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/klog"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
)

type instanceCache struct {
	sync.Mutex
	data map[string]cloudprovider.Instance
}

func (c *instanceCache) addInstances(instances []cloudprovider.Instance) {
	c.Lock()
	for _, i := range instances {
		c.data[i.Id] = i
	}
	c.Unlock()
}

func (c *instanceCache) getInstance(providerID string) (cloudprovider.Instance, bool) {
	c.Lock()
	defer c.Unlock()
	instance, ok := c.data[providerID]
	return instance, ok
}

func (c *instanceCache) deleteInstance(providerID string) {
	c.Lock()
	delete(c.data, providerID)
	c.Unlock()
}

func (c *instanceCache) getData() map[string]cloudprovider.Instance {
	c.Lock()
	defer c.Unlock()
	return c.data
}

func (c *instanceCache) reset() {
	c.Lock()
	defer c.Unlock()
	c.data = map[string]cloudprovider.Instance{}
}

type wipLock struct {
	sync.Mutex
	v bool
}

func (s *wipLock) TrySet() bool {
	s.Lock()
	defer s.Unlock()
	if s.v {
		return false
	}
	s.v = true
	return true
}

func (s *wipLock) UnSet() {
	s.Lock()
	defer s.Unlock()
	s.v = false
}

type NodeGroup struct {
	id         string
	clusterID  string
	clientConf *clientConfObj

	minSize int
	maxSize int

	cache              instanceCache
	deletionInProgress wipLock
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
	nodePool, _, err := n.getValidClientAndPool(n.clusterID, n.id, n.clientConf)
	if err != nil {
		return 0, err
	}
	return int(nodePool.Properties.NodeCount), nil
}

func (n *NodeGroup) getValidClientAndPool(clusterID, nodepoolID string, config *clientConfObj) (*profitbricks.KubernetesNodePool, *iecClient, error) {
	iecConfig, err := config.getIECConfig("")
	if err != nil {
		return nil, nil, errors.Wrap(err, "error getting IECClient config")
	}
	var iecClient *iecClient
	var allInvalid bool
	var nodePool *profitbricks.KubernetesNodePool
	for i, t := range iecConfig.Tokens {
		iecClient = iecClientGetter(t, iecConfig.Endpoint, iecConfig.Insecure)
		nodePool, err = iecClient.GetKubernetesNodePool(n.clusterID, n.id)
		if err != nil {
			if profitbricks.IsStatusUnauthorized(err) {
				klog.V(trace).Infof("Token %d invalid, trying next one", i)
				allInvalid = true
				continue
			}
			return nil, nil, errors.Wrap(err, "failed to get nodepool")
		}
		allInvalid = false
		break
	}
	if allInvalid {
		return nil, nil, errors.New("All tokens invalid")
	}
	return nodePool, iecClient, nil
}

// Increases node group size
func (n *NodeGroup) IncreaseSize(delta int) error {
	klog.V(debug).Infof("Increasing nodegroup %s size, delta: %d", n.id, delta)
	if delta <= 0 {
		return fmt.Errorf("delta must be positive, have: %d", delta)
	}
	nodePool, iecClient, err := n.getValidClientAndPool(n.clusterID, n.id, n.clientConf)
	if err != nil {
		return err
	}
	targetSize := nodePool.Properties.NodeCount + uint32(delta)

	if targetSize > uint32(n.MaxSize()) {
		return fmt.Errorf("size increase is too large. current: %d, desired: %d, max: %d",
			nodePool.Properties.NodeCount, targetSize, n.MaxSize())
	}

	upgradeInput := profitbricks.KubernetesNodePool{
		Properties: &profitbricks.KubernetesNodePoolProperties{
			NodeCount: targetSize,
		},
	}

	_, err = iecClient.UpdateKubernetesNodePool(n.clusterID, n.id, upgradeInput)
	if err != nil {
		return errors.Wrap(err, "failed to update nodepool")
	}

	klog.V(info).Infof("Waiting for %v nodepool to reach target size. Polling every %v.", n.clientConf.iecPollTimeout, n.clientConf.iecPollInterval)
	err = wait.PollImmediate(
		n.clientConf.iecPollInterval,
		n.clientConf.iecPollTimeout,
		iecClient.PollNodePoolNodeCount(n.clusterID, n.id, targetSize))
	if err != nil {
		return errors.Wrap(err, "failed to wait for nodepool update")
	}

	nodePool, err = iecClient.GetKubernetesNodePool(n.clusterID, n.id)
	if err != nil {
		return errors.Wrap(err, "failed to get nodepool")
	}
	return nil
}

// DeleteNodes deletes nodes from this node group (and also decreasing the size
// of the node group with that). Error is returned either on failure or if the
// given node doesn't belong to this node group. This function should wait
// until node group size is updated. Implementation required.
func (n *NodeGroup) DeleteNodes(kubernetesNodes []*apiv1.Node) error {
	// Workaround for KoreAPI bug, dealing with multiple node deletes, may be removed after the bug is fixed.
	if ok := n.deletionInProgress.TrySet(); !ok {
		return fmt.Errorf("error, cannot delete, deletion in progress")
	}
	defer n.deletionInProgress.UnSet()
	klog.V(info).Infof("Deleting %d nodes", len(kubernetesNodes))
	for _, node := range kubernetesNodes {
		klog.V(debug).Infof("Deleting node %s with id %s", node.Name, node.Spec.ProviderID)
		// Use node.Spec.ProviderID as to retrieve nodeID
		nodeID := toNodeID(node.Spec.ProviderID)
		nodePool, iecClient, err := n.getValidClientAndPool(n.clusterID, n.id, n.clientConf)
		if err != nil {
			return err
		}
		if nodePool.Metadata.State != profitbricks.K8sStateActive {
			return fmt.Errorf(
				"failed to delete node %s, nodepool state is %s waiting for state to become ACTIVE again.",
				nodeID, nodePool.Metadata.State)
		}

		_, err = iecClient.DeleteKubernetesNode(n.clusterID, n.id, nodeID)
		if err != nil && !profitbricks.IsStatusNotFound(err) {
			return errors.Wrapf(err, "failed to delete node %s from nodepool %s in cluster %s", nodeID, n.id, n.clusterID)
		}

		targetSize := nodePool.Properties.NodeCount - 1
		err = wait.PollImmediate(
			n.clientConf.iecPollInterval,
			n.clientConf.iecPollTimeout,
			iecClient.PollNodePoolNodeCount(n.clusterID, n.id, targetSize))
		if err != nil {
			return errors.Wrap(err, "failed to wait for nodepool update")
		}
		// Delete node from instance cache
		n.cache.deleteInstance(node.Spec.ProviderID)
		klog.V(info).Infof("Sucessfully deleted %s", node.Name)
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

	nodePool, _, err := n.getValidClientAndPool(n.clusterID, n.id, n.clientConf)
	if err != nil {
		return err
	}
	targetSize := int(nodePool.Properties.NodeCount) + delta
	if targetSize < 0 || targetSize < n.MinSize() {
		return fmt.Errorf("size decrease is too small. current: %d, desired: %d, min: %d",
			nodePool.Properties.NodeCount, targetSize, n.MinSize())
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
	klog.V(info).Infof("Getting nodes for nodegroup: %s in cluster %s", n.id, n.clusterID)
	iecConfig, err := n.clientConf.getIECConfig("")
	if err != nil {
		return nil, errors.Wrap(err, "error getting IECClient config")
	}
	var nodes *profitbricks.KubernetesNodes
	var iecClient *iecClient
	var allInvalid bool
	for i, t := range iecConfig.Tokens {
		iecClient = iecClientGetter(t, iecConfig.Endpoint, iecConfig.Insecure)
		nodes, err = iecClient.ListKubernetesNodes(n.clusterID, n.id)
		if err != nil {
			if profitbricks.IsStatusUnauthorized(err) {
				klog.V(trace).Infof("Token %d invalid, trying next one", i)
				allInvalid = true
				continue
			}
			return nil, errors.Wrapf(err, "failed to get nodes for nodepool %s", n.id)
		}
		allInvalid = false
		break
	}
	if allInvalid {
		return nil, errors.New("All tokens invalid")
	}

	nodeIDs := []string{}
	for _, n := range nodes.Items {
		nodeIDs = append(nodeIDs, n.ID)
	}
	klog.V(debug).Infof("Nodes found: %+v", nodeIDs)
	instances := toInstances(nodes)
	// Add instances to nodegroup's instance cache.
	klog.V(debug).Infof("Updating instance cache of group %s with instances %+v", n.Id(), instances)
	n.cache.addInstances(instances)
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
	nodePool, _, _ := n.getValidClientAndPool(n.clusterID, n.id, n.clientConf)
	return nodePool != nil
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
