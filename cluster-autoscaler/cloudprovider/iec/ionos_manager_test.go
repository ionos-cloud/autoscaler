package iec

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"github.com/stretchr/testify/assert"
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
	confWithNodePools = `---
cluster_id: "12345"
autoscaler_secret_path: "secret-file"
nodepools:
- id: "345"
  autoscaling_limit_max: 5
  autoscaling_limit_min: 1
- id: "456"
  autoscaling_limit_max: 10
  autoscaling_limit_min: 1
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

	t.Run("success, manager with nodepools from config", func(t *testing.T) {
		manager, err := CreateIECManager(strings.NewReader(confWithNodePools))
		assert.NoError(t, err)
		assert.Len(t, manager.GetNodeGroups(), 2)
		assert.Equal(t, "345", manager.GetNodeGroups()[0].id)
		assert.Equal(t, 1, manager.GetNodeGroups()[0].MinSize())
		assert.Equal(t, 5, manager.GetNodeGroups()[0].MaxSize())
		assert.Equal(t, "456", manager.GetNodeGroups()[1].id)
		assert.Equal(t, 1, manager.GetNodeGroups()[1].MinSize())
		assert.Equal(t, 10, manager.GetNodeGroups()[1].MaxSize())
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
		assert.Equal(t, "ionos-secret", manager.(*IECManagerImpl).secretPath, "secret path does not match")
		assert.Equal(t, 3*time.Minute, manager.(*IECManagerImpl).ionosConf.iecPollTimeout, "iec poll timeout does not match")
		assert.Equal(t, 30*time.Second, manager.(*IECManagerImpl).ionosConf.iecPollInterval, "iec poll interval path does not match")
		assert.Equal(t, "default-token", manager.(*IECManagerImpl).ionosConf.defaultToken, "iec default token does not match")
		assert.Equal(t, "api.ionos.com", manager.(*IECManagerImpl).ionosConf.defaultEndpoint, "default endpoint path does not match")
		assert.Equal(t, true, manager.(*IECManagerImpl).ionosConf.insecure, "insecure does not match")
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
