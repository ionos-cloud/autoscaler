package iec

import (
	"testing"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"github.com/stretchr/testify/assert"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
)

var (
	kubernetesNodes = []profitbricks.KubernetesNode{
		{
			ID: "1",
			Metadata: &profitbricks.Metadata{
				State: profitbricks.K8sNodeStateProvisioning,
			},
			Properties: &profitbricks.KubernetesNodeProperties{
				Name: "node1",
			},
		},
		{
			ID: "2",
			Metadata: &profitbricks.Metadata{
				State: profitbricks.K8sNodeStateProvisioned,
			},
			Properties: &profitbricks.KubernetesNodeProperties{
				Name: "node2",
			},
		},
		{
			ID: "3",
			Metadata: &profitbricks.Metadata{
				State: profitbricks.K8sNodeStateRebuilding,
			},
			Properties: &profitbricks.KubernetesNodeProperties{
				Name: "node3",
			},
		},
		{
			ID: "4",
			Metadata: &profitbricks.Metadata{
				State: profitbricks.K8sNodeStateTerminating,
			},
			Properties: &profitbricks.KubernetesNodeProperties{
				Name: "node4",
			},
		},
		{
			ID: "5",
			Metadata: &profitbricks.Metadata{
				State: profitbricks.K8sNodeStateReady,
			},
			Properties: &profitbricks.KubernetesNodeProperties{
				Name: "node5",
			},
		},
	}
	cloudproviderInstances = []cloudprovider.Instance{
		{
			Id: "ionos://1",
			Status: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceCreating,
			},
		}, {
			Id: "ionos://2",
			Status: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceCreating,
			},
		}, {
			Id: "ionos://3",
			Status: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceCreating,
			},
		}, {
			Id: "ionos://4",
			Status: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceDeleting,
			},
		}, {
			Id: "ionos://5",
			Status: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceRunning,
			},
		},
	}
	singleDCConfigMap = map[string]IECConfig{
		"12345": singleTokenConf,
	}
	sDCmTokenConfigMap = map[string]IECConfig{
		"12345": multiTokenConf,
	}
	multiDCConfigMap = map[string]IECConfig{
		"12345": singleTokenConf,
		"54321": multiTokenConf,
	}
)

func TestUtils_ToProviderID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		in := "1-2-3-4"
		want := "ionos://1-2-3-4"
		got := toProviderID(in)
		assert.Equal(t, want, got)
	})
}

func TestUtils_ToNodeId(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		in := "ionos://1-2-3-4"
		want := "1-2-3-4"
		got := toNodeID(in)
		assert.Equal(t, want, got)
	})
}

func TestUtils_ToInstances(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		in := profitbricks.KubernetesNodes{
			Items: kubernetesNodes,
		}
		want := cloudproviderInstances
		got := toInstances(&in)
		assert.Equal(t, want, got)
	})
}

func TestUtils_ToInstance(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		in := profitbricks.KubernetesNode{
			ID: "1",
			Metadata: &profitbricks.Metadata{
				State: profitbricks.K8sNodeStateReady,
			},
		}
		want := cloudprovider.Instance{
			Id: "ionos://1",
			Status: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceRunning,
			},
		}
		got := toInstance(in)
		assert.Equal(t, want, got)
	})
}

func TestUtils_ToInstanceStatus(t *testing.T) {
	tests := []struct {
		in, name string
		want     *cloudprovider.InstanceStatus
	}{
		{
			name: "success, ionos server provisioning",
			in:   profitbricks.K8sNodeStateProvisioning,
			want: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceCreating,
			},
		}, {
			name: "success, ionos server provisioned",
			in:   profitbricks.K8sNodeStateProvisioned,
			want: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceCreating,
			},
		}, {
			name: "success, ionos server rebuiling",
			in:   profitbricks.K8sNodeStateRebuilding,
			want: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceCreating,
			},
		}, {
			name: "success, ionos server terminating",
			in:   profitbricks.K8sNodeStateTerminating,
			want: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceDeleting,
			},
		}, {
			name: "success, ionos server ready",
			in:   profitbricks.K8sNodeStateReady,
			want: &cloudprovider.InstanceStatus{
				State: cloudprovider.InstanceRunning,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := toInstanceStatus(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("Fail, unknown node state", func(t *testing.T) {
		want := &cloudprovider.InstanceStatus{
			State: 0,
			ErrorInfo: &cloudprovider.InstanceErrorInfo{
				ErrorClass:   cloudprovider.OtherErrorClass,
				ErrorCode:    iecErrorCode,
				ErrorMessage: "Unknown node state: wrong_state",
			},
		}
		got := toInstanceStatus("wrong_state")
		assert.Equal(t, want, got)
	})
}

func TestUtils_GetIECConfig(t *testing.T) {
	confObj := &clientConfObj{
		confPath:        "conf-path",
		defaultEndpoint: "api.ionos.com",
		defaultToken:    "default-token",
		insecure:        false,
	}

	t.Run("success, default token and default endpoint", func(t *testing.T) {
		config, err := confObj.getIECConfig("")
		assert.NoError(t, err)
		assert.Equal(t, config.Tokens, []string{"default-token"})
		assert.Equal(t, config.Endpoint, "api.ionos.com")
	})

	t.Run("success, from file content", func(t *testing.T) {
		confObj.defaultToken = ""
		configs = func(confPath string) (map[string]IECConfig, error) {
			return multiDCConfigMap, nil
		}
		config, err := confObj.getIECConfig("54321")
		assert.NoError(t, err)
		assert.Equal(t, config.Tokens, []string{"t1", "t2"})
		configs = getIECConfigs
	})
}
