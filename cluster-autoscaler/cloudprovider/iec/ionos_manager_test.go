package iec

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"github.com/stretchr/testify/assert"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider/iec/mocks"
)

var (
	zero      = uint32(0)
	one       = uint32(1)
	two       = uint32(2)
	three     = uint32(3)
	nodePools = []profitbricks.KubernetesNodePool{
		{
			ID: "1",
			Properties: &profitbricks.KubernetesNodePoolProperties{
				Name:         "nodepool-1",
				NodeCount:    2,
				DatacenterID: "12345",
				AutoScaling: &profitbricks.AutoScaling{
					MinNodeCount: &one,
					MaxNodeCount: &three,
				},
			},
		}, {
			ID: "2",
			Properties: &profitbricks.KubernetesNodePoolProperties{
				Name:         "nodepool-2",
				NodeCount:    2,
				DatacenterID: "12345",
				AutoScaling: &profitbricks.AutoScaling{
					MinNodeCount: &one,
					MaxNodeCount: &two,
				},
			},
		}, {
			ID: "3",
			Properties: &profitbricks.KubernetesNodePoolProperties{
				Name:         "nodepool-3",
				NodeCount:    2,
				DatacenterID: "12345",
				AutoScaling: &profitbricks.AutoScaling{
					MinNodeCount: &zero,
					MaxNodeCount: &zero,
				},
			},
		}, {
			ID: "4",
			Properties: &profitbricks.KubernetesNodePoolProperties{
				Name:      "nodepool-4",
				NodeCount: 2,
			},
		}, {
			ID: "5",
			Properties: &profitbricks.KubernetesNodePoolProperties{
				Name:         "nodepool-5",
				NodeCount:    2,
				DatacenterID: "54321",
				AutoScaling: &profitbricks.AutoScaling{
					MinNodeCount: &one,
					MaxNodeCount: &three,
				},
			},
		}, {
			ID: "6",
			Properties: &profitbricks.KubernetesNodePoolProperties{
				Name:      "nodepool-6",
				NodeCount: 2,
				AutoScaling: &profitbricks.AutoScaling{
					MinNodeCount: nil,
					MaxNodeCount: nil,
				},
			},
		},
	}
	minCfg = `---
cluster_id: "12345"
autoscaler_secret_path: "secret-file"
`
	conf = &clientConfObj{
		defaultToken:    "default-token",
		insecure:        false,
		iecPollTimeout:  time.Millisecond * 100,
		iecPollInterval: time.Millisecond * 10,
	}
	confWOToken = &clientConfObj{
		defaultToken:    "",
		insecure:        false,
		iecPollTimeout:  time.Millisecond * 100,
		iecPollInterval: time.Millisecond * 10,
	}
)

func TestNewManager(t *testing.T) {
	t.Run("success, manager creation minimal config", func(t *testing.T) {
		manager, err := CreateIECManager(strings.NewReader(minCfg))
		assert.NoError(t, err)
		assert.Equal(t, "12345", manager.GetClusterID(), "cluster ID does not match")
	})

	t.Run("success, manager creation from env", func(t *testing.T) {
		os.Setenv(EnvIONOSClusterID, "54321")
		os.Setenv(EnvIONOSSecretPath, "ionos-secret")
		os.Setenv(EnvIONOSPollTimeout, "3m")
		os.Setenv(EnvIONOSPollInterval, "30s")
		os.Setenv(EnvIONOSToken, "default-token")
		os.Setenv(EnvIONOSInsecCon, "true")
		os.Setenv(EnvIONOSEndpoint, "api.ionos.com")
		manager, err := CreateIECManager(nil)
		assert.NoError(t, err)
		assert.Equal(t, "54321", manager.GetClusterID(), "cluster ID does not match")
		os.Unsetenv(EnvIONOSClusterID)
		os.Unsetenv(EnvIONOSSecretPath)
		os.Unsetenv(EnvIONOSPollTimeout)
		os.Unsetenv(EnvIONOSPollInterval)
		os.Unsetenv(EnvIONOSToken)
		os.Unsetenv(EnvIONOSInsecCon)
		os.Unsetenv(EnvIONOSEndpoint)
	})

	t.Run("failure, malformed yaml", func(t *testing.T) {
		_, err := CreateIECManager(strings.NewReader("hello"))
		assert.Error(t, err, "error expected")
	})

	tests := []struct {
		name,
		cfg string
	}{
		{
			name: "failure, empty cluster ID",
			cfg: `---
autoscaler_secret_name: "ionos-secret"
`,
		}, {
			name: "failure, empty autoscaler secret name",
			cfg: `---
cluster_id: "12345"
autoscaler_secret_name: "ionos-secret"
`,
		}, {
			name: "failure, manager creation with wrong pollTimeout",
			cfg: `---
cluster_id: "12345"
autoscaler_secret_name: "ionos-secret"
poll_timeout: "3X"
`,
		}, {
			name: "failure, manager creation with wrong pollTimeout",
			cfg: `---
cluster_id: "12345"
autoscaler_secret_name: "ionos-secret"
poll_interval: "3X"
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateIECManager(strings.NewReader(tc.cfg))
			assert.Error(t, err, "error expected")
		})
	}
}

func TestIECManager_Refresh(t *testing.T) {
	t.Run("default token", func(t *testing.T) {
		manager := IECManagerImpl{
			secretPath: "secret-file",
			clusterID:  "12345",
			ionosConf:  conf,
		}
		ionosClient := mocks.Client{}
		ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
			&profitbricks.KubernetesNodePools{Items: nodePools}, nil).Once()

		iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
			return &iecClient{&ionosClient}
		}

		err := manager.Refresh()
		assert.NoError(t, err)
		// Expect all nodegroups with autoscaling enabled
		assert.Len(t, manager.nodeGroups, 3, "number of nodegroups do not match")
		ionosClient.AssertExpectations(t)
		iecClientGetter = newIECClient
	})

	t.Run("single valid token", func(t *testing.T) {
		manager := IECManagerImpl{
			secretPath: "secret-file",
			clusterID:  "12345",
			ionosConf:  confWOToken,
		}
		configs = func(confPath string) (map[string]IECConfig, error) {
			return singleDCConfigMap, nil
		}

		t.Run("success, some nodegroups", func(t *testing.T) {
			ionosClient := mocks.Client{}
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				&profitbricks.KubernetesNodePools{Items: nodePools}, nil).Once()

			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{&ionosClient}
			}

			err := manager.Refresh()
			assert.NoError(t, err)
			// Expect only nodegroups from dc 12345 with autoscaling enabled
			assert.Len(t, manager.nodeGroups, 3, "number of nodegroups do not match")
			// First nodegroup
			assert.Equalf(t, 1, manager.nodeGroups[0].minSize,
				"minimum node size for nodegroup %s does not match", manager.nodeGroups[0].id)
			assert.Equalf(t, 3, manager.nodeGroups[0].maxSize,
				"maximum node size for nodegroup %s does not match", manager.nodeGroups[0].id)
			// Second nodegroup
			assert.Equalf(t, 1, manager.nodeGroups[1].minSize,
				"minimum node size for nodegroup %s does not match", manager.nodeGroups[0].id)
			assert.Equal(t, 2, manager.nodeGroups[1].maxSize,
				"maximum node size for nodegroup %s does not match", manager.nodeGroups[0].id)
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})

		t.Run("success, no nodepools with autoscaling limits set", func(t *testing.T) {
			ionosClient := mocks.Client{}
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				&profitbricks.KubernetesNodePools{
					Items: nodePools[2:4]}, nil).Once()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{&ionosClient}
			}

			err := manager.Refresh()
			assert.NoError(t, err)
			assert.Len(t, manager.nodeGroups, 0, "number of nodegroups do not match")
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})

		t.Run("success, no nodepools", func(t *testing.T) {
			ionosClient := mocks.Client{}
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				&profitbricks.KubernetesNodePools{
					Items: []profitbricks.KubernetesNodePool{}}, nil).Once()
			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{&ionosClient}
			}

			err := manager.Refresh()
			assert.NoError(t, err)
			assert.Len(t, manager.nodeGroups, 0, "number of nodegroups do not match")
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})

		t.Run("failure, error getting nodepools", func(t *testing.T) {
			ionosClient := mocks.Client{}
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				nil, defaultError).Once()

			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{&ionosClient}
			}
			err := manager.Refresh()
			assert.Error(t, err)
			assert.Nil(t, manager.nodeGroups, "nodegroups must be nil")
			ionosClient.AssertExpectations(t)
			iecClientGetter = newIECClient
		})
		configs = getIECConfigs
	})

	t.Run("multiple tokens", func(t *testing.T) {
		configs = func(confPath string) (map[string]IECConfig, error) {
			return sDCmTokenConfigMap, nil
		}

		t.Run("success, first token invalid", func(t *testing.T) {
			manager := IECManagerImpl{
				secretPath: "secret-file",
				clusterID:  "12345",
				ionosConf:  confWOToken,
			}
			ionosClient := mocks.Client{}
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				nil, profitbricks.ApiError{
					HTTPStatus: http.StatusUnauthorized,
				}).Once()
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				&profitbricks.KubernetesNodePools{Items: nodePools}, nil).Once()

			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{&ionosClient}
			}

			err := manager.Refresh()
			assert.NoError(t, err)
			ionosClient.AssertExpectations(t)
		})

		t.Run("failure, all tokens invalid", func(t *testing.T) {
			manager := IECManagerImpl{
				secretPath: "secret-file",
				clusterID:  "12345",
				ionosConf:  confWOToken,
			}
			ionosClient := mocks.Client{}
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				nil, profitbricks.ApiError{
					HTTPStatus: http.StatusUnauthorized,
				}).Once()
			ionosClient.On("ListKubernetesNodePools", manager.clusterID).Return(
				nil, profitbricks.ApiError{
					HTTPStatus: http.StatusUnauthorized,
				}).Once()

			iecClientGetter = func(token, endpoint string, insecure bool) *iecClient {
				return &iecClient{&ionosClient}
			}

			err := manager.Refresh()
			assert.Error(t, err)
			assert.Nil(t, manager.nodeGroups, "nodegroups must be nil")
			assert.Contains(t, err.Error(), "errors for all tokens")
			ionosClient.AssertExpectations(t)
		})
		configs = getIECConfigs
	})
}
