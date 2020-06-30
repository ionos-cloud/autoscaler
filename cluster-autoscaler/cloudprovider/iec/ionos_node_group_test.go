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
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider/iec/mocks"
)

var (
	singleTokenConf = IECConfig{
		Tokens:   []string{"t1"},
		Endpoint: "api.ionos.com",
		Insecure: false,
	}
	multiTokenConf = IECConfig{
		Tokens:   []string{"t1", "t2"},
		Endpoint: "api.ionos.com",
	}
)

func testNodeGroup(clientConf *clientConfObj, inp *profitbricks.KubernetesNodePool) NodeGroup {
	var minNodes, maxNodes int
	var id string
	if inp != nil {
		minNodes = int(*inp.Properties.AutoScaling.MinNodeCount)
		maxNodes = int(*inp.Properties.AutoScaling.MaxNodeCount)
	}
	if inp != nil {
		id = inp.ID
	}

	return NodeGroup{
		id:         id,
		clusterID:  "12345",
		clientConf: clientConf,
		nodePool:   inp,
		minSize:    minNodes,
		maxSize:    maxNodes,
	}
}

func initIONOSNodePool(nodecount, min, max uint32, id, state string) *profitbricks.KubernetesNodePool {
	np := &profitbricks.KubernetesNodePool{
		Properties: &profitbricks.KubernetesNodePoolProperties{
			NodeCount: nodecount,
			AutoScaling: &profitbricks.AutoScaling{
				MinNodeCount: &min,
				MaxNodeCount: &max,
			},
		},
		Metadata: &profitbricks.Metadata{
			State: profitbricks.K8sStateActive,
		},
	}
	if id != "" {
		np.ID = id
	}
	return np
}

func initNodeGroup(size, min, max uint32, clientConf *clientConfObj) NodeGroup {
	if clientConf == nil {
		clientConf = &clientConfObj{
			defaultToken:    "default-token",
			iecPollTimeout:  100 * time.Millisecond,
			iecPollInterval: 10 * time.Millisecond,
		}
	}
	return testNodeGroup(clientConf, initIONOSNodePool(size, min, max, "123", profitbricks.StateAvailable))
}

func TestNodeGroup_Target_size(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		size, err := ng.TargetSize()
		assert.NoError(t, err)
		assert.EqualValues(t, numberOfNodes, size, "target size is not correct")
	})
}

func TestNodeGroup_IncreaseSize(t *testing.T) {
	t.Run("successfully increase to maximum", func(t *testing.T) {
		delta := uint32(7)
		numberOfNodes := uint32(3)
		ionosClient := &mocks.Client{}
		ng := initNodeGroup(numberOfNodes, 1, 10, nil)

		newCount := numberOfNodes + delta
		update := &profitbricks.KubernetesNodePool{
			Properties: &profitbricks.KubernetesNodePoolProperties{
				NodeCount: newCount,
			},
		}
		updated := initIONOSNodePool(newCount, 1, 10, "123", profitbricks.StateAvailable)

		ionosClient.On("UpdateKubernetesNodePool", ng.clusterID, ng.id, *update).
			Return(updated, nil).Once()
		// Poll 5 times before it became true
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(ng.nodePool, nil).Times(5)
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(updated, nil).Twice()
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		err := ng.IncreaseSize(int(delta))
		assert.NoError(t, err)
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("failure, negative increase", func(t *testing.T) {
		delta := -1
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		err := ng.IncreaseSize(delta)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "delta must be positive", "wrong error")
	})

	t.Run("failed, to get iec config", func(t *testing.T) {
		configs = func(confPath string) (map[string]IECConfig, error) {
			return nil, defaultError
		}
		ng := initNodeGroup(uint32(2), 1, 3, &clientConfObj{})
		err := ng.IncreaseSize(1)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "error getting IECClient config", "wrong error")
		configs = getIECConfigs
	})

	t.Run("failure, zero increase", func(t *testing.T) {
		delta := 0
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		err := ng.IncreaseSize(delta)
		assert.Error(t, err, "expected error")
		assert.Contains(t, err.Error(), "delta must be positive", "wrong error")
	})

	t.Run("failure, increase above maximum", func(t *testing.T) {
		delta := 8
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 10, nil)

		err := ng.IncreaseSize(delta)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "size increase is too large", "wrong error")
	})

	t.Run("failure, PollNodePoolNodeCount fails", func(t *testing.T) {
		delta := uint32(2)
		numberOfNodes := uint32(1)
		ionosClient := &mocks.Client{}
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		newCount := numberOfNodes + delta
		update := &profitbricks.KubernetesNodePool{
			Properties: &profitbricks.KubernetesNodePoolProperties{
				NodeCount: newCount,
			},
		}
		updated := initIONOSNodePool(newCount, 1, 3, "123", profitbricks.StateAvailable)

		pollError := errors.New("Oops something went wrong")

		ionosClient.On("UpdateKubernetesNodePool", ng.clusterID, ng.id, *update).
			Return(updated, nil).Once()
		// Poll 5 times before it errors
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(ng.nodePool, nil).Times(4)
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(nil, pollError).Once()
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		err := ng.IncreaseSize(int(delta))
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "failed to wait for nodepool update", "wrong error")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("failure, update kubernetes nodepool poll fails", func(t *testing.T) {
		delta := uint32(2)
		numberOfNodes := uint32(1)
		ionosClient := &mocks.Client{}
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		newCount := numberOfNodes + delta
		update := &profitbricks.KubernetesNodePool{
			Properties: &profitbricks.KubernetesNodePoolProperties{
				NodeCount: newCount,
			},
		}
		updated := initIONOSNodePool(newCount, 1, 3, "123", profitbricks.StateAvailable)

		ionosClient.On("UpdateKubernetesNodePool", ng.clusterID, ng.id, *update).
			Return(updated, nil).Once()
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(nil, defaultError)
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		err := ng.IncreaseSize(int(delta))
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "failed to wait for nodepool update:", "Wrong error")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("failure, update kubernetes nodepool failed", func(t *testing.T) {
		delta := 1
		ionosClient := &mocks.Client{}
		ionosClient.On("UpdateKubernetesNodePool", mock.Anything, mock.Anything, mock.Anything).Return(
			nil, defaultError).Once()
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}
		ng := initNodeGroup(2, 1, 3, nil)

		err := ng.IncreaseSize(delta)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "failed to update nodepool:", "wrong error")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("failure, failed to finally get the nodepool", func(t *testing.T) {
		delta := uint32(2)
		numberOfNodes := uint32(3)
		ionosClient := &mocks.Client{}
		ng := initNodeGroup(numberOfNodes, 1, 5, nil)

		newCount := numberOfNodes + delta
		update := &profitbricks.KubernetesNodePool{
			Properties: &profitbricks.KubernetesNodePoolProperties{
				NodeCount: newCount,
			},
		}
		updated := initIONOSNodePool(newCount, 1, 5, "123", profitbricks.StateAvailable)
		ionosClient.On("UpdateKubernetesNodePool", ng.clusterID, ng.id, *update).
			Return(updated, nil).Once()
		// Poll 5 times before it became true
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(ng.nodePool, nil).Times(5)
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(updated, nil).Once()
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(nil, defaultError).Once()
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		err := ng.IncreaseSize(int(delta))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get nodepool", "wrong error")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("multiple tokens", func(t *testing.T) {
		configs = func(confPath string) (map[string]IECConfig, error) {
			return sDCmTokenConfigMap, nil
		}
		t.Run("success, first token invalid", func(t *testing.T) {
			delta := uint32(1)
			numberOfNodes := uint32(1)
			ionosClient := &mocks.Client{}
			ng := initNodeGroup(numberOfNodes, 1, 3, &clientConfObj{})

			newCount := numberOfNodes + delta
			update := &profitbricks.KubernetesNodePool{
				Properties: &profitbricks.KubernetesNodePoolProperties{
					NodeCount: newCount,
				},
			}
			updated := initIONOSNodePool(newCount, 1, 10, "123", profitbricks.StateAvailable)
			// First token invalid
			ionosClient.On("UpdateKubernetesNodePool", ng.clusterID, ng.id, *update).
				Return(nil, profitbricks.ApiError{HTTPStatus: http.StatusUnauthorized}).Once()
			// Second is fine
			ionosClient.On("UpdateKubernetesNodePool", ng.clusterID, ng.id, *update).
				Return(updated, nil).Once()
			ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
				Return(updated, nil).Twice()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{ionosClient}
			}

			err := ng.IncreaseSize(int(delta))
			assert.NoError(t, err)
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})

		t.Run("failure, all tokens invalid", func(t *testing.T) {
			delta := uint32(1)
			numberOfNodes := uint32(1)
			ionosClient := &mocks.Client{}
			ng := initNodeGroup(numberOfNodes, 1, 3, &clientConfObj{})

			newCount := numberOfNodes + delta
			update := &profitbricks.KubernetesNodePool{
				Properties: &profitbricks.KubernetesNodePoolProperties{
					NodeCount: newCount,
				},
			}
			// First token invalid
			ionosClient.On("UpdateKubernetesNodePool", ng.clusterID, ng.id, *update).
				Return(nil, profitbricks.ApiError{HTTPStatus: http.StatusUnauthorized}).Twice()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{ionosClient}
			}

			err := ng.IncreaseSize(int(delta))
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "All tokens invalid", "wrong error")
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})
		configs = getIECConfigs
	})
}

func TestNodeGroup_DecreaseTargetSize(t *testing.T) {
	t.Run("failure, unsupported", func(t *testing.T) {
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		err := ng.DecreaseTargetSize(-1)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "Currently not supported behavior", "wrong error")
	})

	t.Run("failure, positive decrease", func(t *testing.T) {
		delta := 2
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		err := ng.DecreaseTargetSize(delta)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "delta must be negative", "wrong error")
	})

	t.Run("failure, zero decrease", func(t *testing.T) {
		delta := 0
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		err := ng.DecreaseTargetSize(delta)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "delta must be negative", "wrong error")
	})

	t.Run("failure, decrease below minimum", func(t *testing.T) {
		delta := -3
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		err := ng.DecreaseTargetSize(delta)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "size decrease is too small.", "wrong error")
	})
}

func TestNodeGroup_DeleteNodes(t *testing.T) {
	nodes := []*corev1.Node{
		{
			Spec: corev1.NodeSpec{
				ProviderID: "ionos://1",
			},
		},
		{
			Spec: corev1.NodeSpec{
				ProviderID: "ionos://2",
			},
		},
		{
			Spec: corev1.NodeSpec{
				ProviderID: "ionos://3",
			},
		},
	}

	t.Run("success", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ng := initNodeGroup(4, 1, 4, nil)
		count := ng.nodePool.Properties.NodeCount
		min := *ng.nodePool.Properties.AutoScaling.MinNodeCount
		max := *ng.nodePool.Properties.AutoScaling.MaxNodeCount
		id := ng.nodePool.ID
		state := ng.nodePool.Metadata.State
		// Delete first node,
		ionosClient.On("DeleteKubernetesNode", ng.clusterID, ng.id, "1").Return(&http.Header{}, nil).Once()
		// Poll 5 times before success
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(initIONOSNodePool(count, min, max, id, state), nil).Times(5)
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(initIONOSNodePool(count-1, min, max, id, state), nil).Once()
		// Delete second node
		ionosClient.On("DeleteKubernetesNode", ng.clusterID, ng.id, "2").Return(&http.Header{}, nil).Once()
		// Poll 5 times before success
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(initIONOSNodePool(count-1, min, max, id, state), nil).Times(5)
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(initIONOSNodePool(count-2, min, max, id, state), nil).Once()
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}
		err := ng.DeleteNodes(nodes[0:2])
		assert.NoError(t, err)
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("ionosClient node deletion fails", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ng := initNodeGroup(4, 1, 4, nil)
		// Fail on first node
		ionosClient.On("DeleteKubernetesNode", ng.clusterID, ng.id, "1").
			Return(&http.Header{}, errors.New("oops something went wrong.")).Once()
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}
		err := ng.DeleteNodes(nodes)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete node", "wrong error")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("ionosClient poll fails", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ng := initNodeGroup(3, 1, 3, nil)
		// Delete first node
		ionosClient.On("DeleteKubernetesNode", ng.clusterID, ng.id, "1").Return(&http.Header{}, nil).Once()
		// Poll never succeeds
		ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
			Return(nil, defaultError)
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		err := ng.DeleteNodes(nodes)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to wait for nodepool update:")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("failed, to get iec config", func(t *testing.T) {
		configs = func(confPath string) (map[string]IECConfig, error) {
			return nil, defaultError
		}
		ng := initNodeGroup(uint32(2), 1, 3, &clientConfObj{})
		err := ng.DeleteNodes(nodes)
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "error getting IECClient config", "wrong error")
		configs = getIECConfigs
	})

	t.Run("multiple Tokens", func(t *testing.T) {
		configs = func(confPath string) (map[string]IECConfig, error) {
			return sDCmTokenConfigMap, nil
		}
		t.Run("success, first token invalid", func(t *testing.T) {
			ionosClient := &mocks.Client{}
			ng := initNodeGroup(4, 1, 4, &clientConfObj{})
			count := ng.nodePool.Properties.NodeCount
			min := *ng.nodePool.Properties.AutoScaling.MinNodeCount
			max := *ng.nodePool.Properties.AutoScaling.MaxNodeCount
			id := ng.nodePool.ID
			state := ng.nodePool.Metadata.State
			// Delete node,
			ionosClient.On("DeleteKubernetesNode", ng.clusterID, ng.id, "1").Return(
				nil, profitbricks.ApiError{HTTPStatus: http.StatusUnauthorized}).Once()
			ionosClient.On("DeleteKubernetesNode", ng.clusterID, ng.id, "1").Return(&http.Header{}, nil).Once()
			ionosClient.On("GetKubernetesNodePool", ng.clusterID, ng.nodePool.ID).
				Return(initIONOSNodePool(count-1, min, max, id, state), nil).Once()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{ionosClient}
			}
			err := ng.DeleteNodes(nodes[0:1])
			assert.NoError(t, err)
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})

		t.Run("failed, all tokens invalid", func(t *testing.T) {
			ionosClient := &mocks.Client{}
			ng := initNodeGroup(4, 1, 4, &clientConfObj{})
			// Delete node,
			ionosClient.On("DeleteKubernetesNode", ng.clusterID, ng.id, "1").Return(
				nil, profitbricks.ApiError{HTTPStatus: http.StatusUnauthorized}).Twice()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{ionosClient}
			}
			err := ng.DeleteNodes(nodes[0:1])
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "All tokens invalid", "wrong error")
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})
		configs = getIECConfigs
	})
}

func TestNodeGroup_Nodes(t *testing.T) {
	numberOfNodes := uint32(5)
	ng := initNodeGroup(numberOfNodes, 1, 5, nil)

	t.Run("success, with state mapping", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ionosClient.On("ListKubernetesNodes", ng.clusterID, ng.nodePool.ID).Return(
			&profitbricks.KubernetesNodes{
				Items: kubernetesNodes,
			}, nil).Once()

		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		nodes, err := ng.Nodes()
		assert.NoError(t, err)
		assert.Equal(t, cloudproviderInstances, nodes, "nodes do not match")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("failure (nil node pool)", func(t *testing.T) {
		ng := testNodeGroup(&clientConfObj{}, nil)

		res, err := ng.Nodes()
		assert.Nil(t, res, "expected nil")
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "node pool instance is not created", "wrong error")
	})

	t.Run("failure (iecClient GetNodes fails)", func(t *testing.T) {
		ionosClient := &mocks.Client{}
		ionosClient.On("ListKubernetesNodes", ng.clusterID, ng.id).
			Return(nil, errors.New("oops something went wrong"))
		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{ionosClient}
		}

		res, err := ng.Nodes()
		assert.Nil(t, res, "expected nil")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get nodes for nodepool", "wrong error")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("failed, to get iec config", func(t *testing.T) {
		configs = func(confPath string) (map[string]IECConfig, error) {
			return nil, defaultError
		}
		ng := initNodeGroup(uint32(2), 1, 3, &clientConfObj{})
		res, err := ng.Nodes()
		assert.Nil(t, res, "expected nil")
		assert.Error(t, err, "error expected")
		assert.Contains(t, err.Error(), "error getting IECClient config", "wrong error")
		configs = getIECConfigs
	})

	t.Run("multiple tokens", func(t *testing.T) {
		t.Run("success, first token invalid", func(t *testing.T) {
			ionosClient := &mocks.Client{}
			configs = func(confPath string) (map[string]IECConfig, error) {
				return sDCmTokenConfigMap, nil
			}
			ng := initNodeGroup(1, 1, 5, &clientConfObj{})
			ionosClient.On("ListKubernetesNodes", ng.clusterID, ng.nodePool.ID).Return(
				nil, profitbricks.ApiError{HTTPStatus: http.StatusUnauthorized}).Once()
			ionosClient.On("ListKubernetesNodes", ng.clusterID, ng.nodePool.ID).Return(
				&profitbricks.KubernetesNodes{
					Items: kubernetesNodes,
				}, nil).Once()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{ionosClient}
			}

			nodes, err := ng.Nodes()
			assert.NoError(t, err)
			assert.Equal(t, cloudproviderInstances, nodes, "nodes do not match")
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})

		t.Run("failed, all tokens invalid", func(t *testing.T) {
			ionosClient := &mocks.Client{}
			ng := initNodeGroup(4, 1, 4, &clientConfObj{})
			// Delete node,
			ionosClient.On("ListKubernetesNodes", ng.clusterID, ng.id).Return(
				nil, profitbricks.ApiError{HTTPStatus: http.StatusUnauthorized}).Twice()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{ionosClient}
			}
			_, err := ng.Nodes()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "All tokens invalid", "wrong error")
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})
		configs = getIECConfigs
	})
}

func TestNodeGroup_Debug(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)

		d := ng.Debug()
		exp := "cluster ID: 12345, nodegroup ID: 123 (min: 1, max: 3)"
		assert.Equal(t, exp, d, "debug string do not match")
	})
}

func TestNodeGroup_Exist(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		numberOfNodes := uint32(3)
		ng := initNodeGroup(numberOfNodes, 1, 3, nil)
		exist := ng.Exist()
		assert.Equal(t, true, exist, "node pool should exist")
	})

	t.Run("failure", func(t *testing.T) {
		ng := testNodeGroup(&clientConfObj{}, nil)
		exist := ng.Exist()
		assert.Equal(t, false, exist, "node pool should not exist")
	})
}

func TestNodeGroup_TemplateNodeInfo(t *testing.T) {
	ng := initNodeGroup(1, 1, 3, nil)
	ret, err := ng.TemplateNodeInfo()
	assert.Nil(t, ret)
	assert.Equal(t, err, cloudprovider.ErrNotImplemented)
}

func TestNodeGroup_Autoprovisioned(t *testing.T) {
	ng := initNodeGroup(1, 1, 3, nil)
	ret := ng.Autoprovisioned()
	assert.False(t, ret)
}

func TestNodeGroup_Create(t *testing.T) {
	ng := initNodeGroup(1, 1, 3, nil)
	ret, err := ng.Create()
	assert.Nil(t, ret)
	assert.Equal(t, err, cloudprovider.ErrNotImplemented)
}

func TestNodeGroup_Delete(t *testing.T) {
	ng := initNodeGroup(1, 1, 3, nil)
	err := ng.Delete()
	assert.Equal(t, err, cloudprovider.ErrNotImplemented)
}
