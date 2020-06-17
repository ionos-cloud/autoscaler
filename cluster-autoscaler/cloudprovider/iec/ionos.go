package iec

import (
	"crypto/tls"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

type iecClient struct {
	Client
}

var _ Client = &profitbricks.Client{}

var iecClientGetter = newIECClient

func newIECClient(token, endpoint string, insecure bool) *iecClient {
	klog.V(4).Infof("Setting up IEC iecClient, url: %s", endpoint)
	ionosClient := profitbricks.NewClientbyToken(token)
	if endpoint != "" {
		ionosClient.SetCloudApiURL(endpoint)
	}
	if insecure {
		ionosClient.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: insecure})
	}
	return &iecClient{Client: ionosClient}
}

func (i *iecClient) PollNodePoolNodeCount(clusterID, nodepoolID string, targetSize uint32) wait.ConditionFunc {
	return func() (bool, error) {
		klog.V(5).Infof("Polling for nodepool: %s, cluster: %s", nodepoolID, clusterID)
		np, err := i.GetKubernetesNodePool(clusterID, nodepoolID)
		if err != nil {
			klog.Errorf("Error getting nodepool: %s for cluster: %s", nodepoolID, clusterID)
			return false, err
		}
		klog.V(5).Infof("State: %s, nodecount got: %d, want %d", np.Metadata.State, np.Properties.NodeCount, targetSize)
		if np.Metadata.State == profitbricks.K8sStateAcvtive && np.Properties.NodeCount == targetSize {
			return true, nil
		}
		return false, nil
	}
}
