/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package iec

import (
	"fmt"
	"io"
	"testing"

	"github.com/pkg/errors"
	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider/iec/mocks"
	"k8s.io/autoscaler/cluster-autoscaler/config"
	"k8s.io/klog"
)

var (
	defaultNode = profitbricks.KubernetesNode{
		ID: "1",
		Metadata: &profitbricks.Metadata{
			State: profitbricks.StateAvailable,
		},
	}
	defaultError = errors.New("oops, something went wrong")
)

func initializedManager(clientConf *clientConfObj) *IECManagerImpl {
	return &IECManagerImpl{
		clusterID: "12345",
		ionosConf: clientConf,
		nodeGroups: []*NodeGroup{
			{
				id:        "1",
				clusterID: "12345",
				nodePool: &profitbricks.KubernetesNodePool{
					ID:         "1",
					Properties: &profitbricks.KubernetesNodePoolProperties{DatacenterID: "12345"},
				},
				clientConf: clientConf,
				minSize:    1,
				maxSize:    3,
				cache:      instanceCache{data: make(map[string]cloudprovider.Instance)},
			},
		},
	}
}

func testCloudProvider(clientConf *clientConfObj, manager IECManager) *IECCloudProvider {
	if clientConf == nil {
		clientConf = conf
	}
	if manager == nil {
		manager = initializedManager(clientConf)
	}
	rl := &cloudprovider.ResourceLimiter{}

	provider := BuildIECCloudProvider(manager, rl)
	return provider
}

func TestNewIECCloudProvider(t *testing.T) {
	t.Run("cloud provider creation success", func(t *testing.T) {
		_ = testCloudProvider(nil, nil)
	})
}

func TestIECCloudProvider_Name(t *testing.T) {
	provider := testCloudProvider(nil, nil)

	t.Run("cloud provider name success", func(t *testing.T) {
		name := provider.Name()
		assert.Equal(t, cloudprovider.IECProviderName, name, "provider name does not match")
	})
}

func TestIECCloudProvider_NodeGroups(t *testing.T) {
	provider := testCloudProvider(nil, nil)

	t.Run("#node pool match", func(t *testing.T) {
		nodeGroups := provider.NodeGroups()
		assert.Equal(t, 1, len(nodeGroups), "number of node pools does not match")
		for i, n := range nodeGroups {
			assert.Equalf(t, n, provider.manager.GetNodeGroups()[i], "returned nodegroup %d does not match")
		}
	})
}

func TestIECCloudProvider_NodeGroupForNode(t *testing.T) {
	configs = func(confPath string) (map[string]IECConfig, error) {
		return singleDCConfigMap, nil
	}

	t.Run("success, no nodegroup found for node", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ionosClient.On("ListKubernetesNodes", "12345", "1").Return(
			&profitbricks.KubernetesNodes{Items: []profitbricks.KubernetesNode{
				defaultNode,
			}}, nil)
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		provider := testCloudProvider(nil, nil)
		// try to get nodegroup for node 10
		node := &apiv1.Node{
			Spec: apiv1.NodeSpec{
				ProviderID: toProviderID("10"),
			},
		}
		nodeGroup, err := provider.NodeGroupForNode(node)
		assert.NoError(t, err)
		assert.Nil(t, nodeGroup)
		ionosClient.AssertExpectations(t)
	})

	t.Run("success", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ionosClient.On("ListKubernetesNodes", "12345", "1").Return(
			&profitbricks.KubernetesNodes{Items: []profitbricks.KubernetesNode{
				defaultNode,
			}}, nil)
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		provider := testCloudProvider(nil, nil)
		// try to get the nodeGroup for the node with ID 2
		node := &apiv1.Node{
			Spec: apiv1.NodeSpec{
				ProviderID: toProviderID("1"),
			},
		}

		nodeGroup, err := provider.NodeGroupForNode(node)
		assert.NoError(t, err)
		assert.Equalf(t, nodeGroup.Id(), "1", "expected nodegroup id 1, got %d", nodeGroup.Id())
		ionosClient.AssertExpectations(t)
	})

	t.Run("success, found in cache", func(t *testing.T) {
		provider := testCloudProvider(nil, nil)
		provider.manager.GetNodeGroups()[0].cache.data = map[string]cloudprovider.Instance{
			toProviderID("1"): cloudprovider.Instance{},
		}
		// try to get the nodeGroup for the node with ID 1
		node := &apiv1.Node{
			Spec: apiv1.NodeSpec{
				ProviderID: toProviderID("1"),
			},
		}

		nodeGroup, err := provider.NodeGroupForNode(node)
		assert.NoError(t, err)
		assert.Equalf(t, "1", nodeGroup.Id(), "expected nodegroup id 1, got %s", nodeGroup.Id())
	})

	t.Run("success, single token", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ionosClient.On("ListKubernetesNodes", "12345", "1").Return(
			&profitbricks.KubernetesNodes{Items: []profitbricks.KubernetesNode{
				defaultNode,
			}}, nil)
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		provider := testCloudProvider(nil, nil)
		// try to get the nodeGroup for the node with ID 2
		node := &apiv1.Node{
			Spec: apiv1.NodeSpec{
				ProviderID: toProviderID("1"),
			},
		}

		nodeGroup, err := provider.NodeGroupForNode(node)
		assert.NoError(t, err)
		assert.Equalf(t, nodeGroup.Id(), "1", "expected nodegroup id 1, got %d", nodeGroup.Id())
		ionosClient.AssertExpectations(t)
	})

	t.Run("fail, nodeGroupForNode error", func(t *testing.T) {
		// replace method mock in provider
		ionosClient := &mocks.Client{}
		ionosClient.On("ListKubernetesNodes", mock.Anything, mock.Anything).Return(
			nil, errors.New("oops something went wrong"))
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		provider := testCloudProvider(nil, nil)

		node := &apiv1.Node{
			Spec: apiv1.NodeSpec{
				ProviderID: toProviderID("node-1-2"),
			},
		}

		nodegroup, err := provider.NodeGroupForNode(node)
		assert.Nil(t, nodegroup)
		assert.Error(t, err)
		ionosClient.AssertExpectations(t)
	})
	configs = getIECConfigs
}

func TestIECCloudProvider_GetAvailableGPUTypes(t *testing.T) {
	provider := testCloudProvider(nil, nil)
	gpuTypes := provider.GetAvailableGPUTypes()
	assert.Empty(t, gpuTypes)
}

func TestIECCloudProvider_GetAvailableMachineTypes(t *testing.T) {
	provider := testCloudProvider(nil, nil)
	machineTypes, err := provider.GetAvailableMachineTypes()
	assert.Empty(t, machineTypes)
	assert.NoError(t, err)
}

func TestIECCloudProvider_GetResourceLimiter(t *testing.T) {
	provider := testCloudProvider(nil, nil)
	rl, err := provider.GetResourceLimiter()
	assert.NoError(t, err)
	assert.Equal(t, provider.resourceLimiter, rl)
}

func TestIECCloudProvider_NewNodeGroup(t *testing.T) {
	provider := testCloudProvider(nil, nil)
	nodeGroup, err := provider.NewNodeGroup(
		"",
		map[string]string{},
		map[string]string{},
		[]apiv1.Taint{},
		map[string]resource.Quantity{})
	assert.Nil(t, nodeGroup)
	assert.Equal(t, err, cloudprovider.ErrNotImplemented)
}

func TestIECCloudProvider_GPULabel(t *testing.T) {
	provider := testCloudProvider(nil, nil)
	label := provider.GPULabel()
	assert.Equal(t, "", label)
}

func TestIECCloudProvider_Pricing(t *testing.T) {
	provider := testCloudProvider(nil, nil)
	_, err := provider.Pricing()
	assert.Equal(t, err, cloudprovider.ErrNotImplemented)
}

func TestIECCloudProvider_Cleanup(t *testing.T) {
	provider := testCloudProvider(nil, nil)
	ret := provider.Cleanup()
	assert.Nil(t, ret)
}

func TestIECCloudProvider_Refresh(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		iecManagerMock := &MockIECManager{}
		iecManagerMock.On("Refresh").Return(nil)
		provider := testCloudProvider(nil, iecManagerMock)
		err := provider.Refresh()
		assert.NoError(t, err)
		iecManagerMock.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		iecManagerMock := &MockIECManager{}
		iecManagerMock.On("Refresh").Return(defaultError)
		provider := testCloudProvider(nil, iecManagerMock)
		err := provider.Refresh()
		assert.Error(t, err)
		iecManagerMock.AssertExpectations(t)
	})
}

func logFatalTest(format string, args ...interface{}) {
	panic(fmt.Sprintf(format, args...))
}

func TestBuildIEC(t *testing.T) {
	opts := config.AutoscalingOptions{CloudConfig: "test-file"}
	do := cloudprovider.NodeGroupDiscoveryOptions{}
	rl := &cloudprovider.ResourceLimiter{}
	manager := &IECManagerImpl{
		clusterID:  "12345",
		nodeGroups: nil,
	}

	t.Run("succeed", func(t *testing.T) {
		read = func(file string) ([]byte, error) {
			return nil, nil
		}
		createIECManager = func(configReader io.Reader) (IECManager, error) {
			return manager, nil
		}
		provider := BuildIEC(opts, do, rl)
		assert.Equal(t, &IECCloudProvider{
			manager:         manager,
			resourceLimiter: rl,
		}, provider)
		read = readFile
		createIECManager = CreateIECManager
	})

	logFatal = logFatalTest

	t.Run("failed to read config file", func(t *testing.T) {
		read = func(file string) ([]byte, error) {
			return nil, defaultError
		}
		assert.PanicsWithValue(t, "Couldn't read cloud provider configuration test-file: oops, something went wrong", func() {
			BuildIEC(opts, do, rl)
		})
		read = readFile
	})

	t.Run("failed to create manager", func(t *testing.T) {
		read = func(file string) ([]byte, error) {
			return nil, nil
		}
		createIECManager = func(configReader io.Reader) (IECManager, error) {
			return nil, defaultError
		}
		assert.PanicsWithValue(t, "Failed to create Ionos Enterprise manager: oops, something went wrong", func() {
			BuildIEC(opts, do, rl)
		})
		read = readFile
		createIECManager = CreateIECManager
	})

	logFatal = klog.Fatalf
}
