package iec

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"gopkg.in/yaml.v2"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/klog"
)

// IECManagerImpl handles Ionos Enterprise Cloud communication and data caching of
// node groups (node pools in IEC)
type IECManagerImpl struct {
	IECManager
	secretPath string
	clusterID  string
	ionosConf  *clientConfObj
	nodeGroups []*NodeGroup
	interrupt  chan struct{}
}

type clientConfObj struct {
	confPath, defaultEndpoint, defaultToken string
	insecure                                bool
	iecPollTimeout, iecPollInterval         time.Duration
}

const (
	EnvIONOSSecretPath   = "IONOS_SECRET_PATH"
	EnvIONOSClusterID    = "IONOS_CLUSTER_ID"
	EnvIONOSPollTimeout  = "IONOS_POLL_TIMEOUT"
	EnvIONOSPollInterval = "IONOS_POLL_INTERVAL"
	EnvIONOSToken        = "IONOS_DEFAULT_TOKEN"
	EnvIONOSInsecCon     = "IONOS_CONNECTION_INSECURE"
	EnvIONOSEndpoint     = "IONOS_DEFAULT_ENDPOINT"
	DefaultTimeout       = 30 * time.Minute
	DefaultInterval      = 30 * time.Second
)

//go:generate mockery -name IECManager -inpkg -case snake -dir ./cloudprovider/iec -output ./cloudprovider/iec -testonly
type IECManager interface {
	// Refresh triggers refresh of cached resources.
	Refresh() error
	// Cleanup cleans up open resources before the cloud provider is destroyed, i.e. go routines etc.
	Cleanup()
	// GetNodesGroups
	GetNodeGroups() []*NodeGroup
	// GetClusterID
	GetClusterID() string
}

type Config struct {
	// IonosDefaultEndpoint to override the default Ionos Endoint used by the IONOS Client in the Cluster Autoscaler
	IONOSDefaultEndpoint string `yaml:"ionos_default_endpoint"`

	// ClusterID is the id associated with the cluster where Ionos Enterprise Cloud
	// Cluster Autoscaler is running.
	ClusterID string `yaml:"cluster_id"`

	// PollTimeout is the timeout for polling a nodegroup after an update, e.g.
	// decreasing/increasing nodecount, until this update should have taken place.
	PollTimeout time.Duration `yaml:"poll_timeout"`

	// PollInterval is the interval in which a nodegroup is polled after an update,
	// decreasing/increasing nodecount
	PollInterval time.Duration `yaml:"poll_interval"`

	// AutoscalerSecretPath is the path to the ionos-iecClient config file mounted from the secret.
	// File contains get token and endpoint information for the IONOS CloudAPI iecClient.
	AutoscalerSecretPath string `yaml:"autoscaler_secret_path"`

	// DefaultIONOSToken is an IONOS cloud api access token. If set all ionos iecClient creations
	// default to this token. Secret content AutoscalerSecretPath points to will be ignored.
	DefaultIONOSToken string `yaml:"default_ionos_token"`

	// IONOSConnectionInsecure whether the iecClient created from the default token should us an insecure connection.
	// Only applied if DefaultIONOSToken is set.
	DefaultIONOSConInsecure bool `yaml:"ionos_connection_insecure"`
}

func (c *Config) valid() (err error) {
	klog.V(debug).Info("Processing config")
	var result *multierror.Error
	if c.IONOSDefaultEndpoint == "" {
		if value, ok := os.LookupEnv(EnvIONOSEndpoint); ok {
			c.IONOSDefaultEndpoint = value
		}
	}

	if c.DefaultIONOSToken == "" {
		if value, ok := os.LookupEnv(EnvIONOSToken); ok {
			c.DefaultIONOSToken = value
		}
	}

	if value, ok := os.LookupEnv(EnvIONOSInsecCon); ok {
		v, _ := strconv.ParseBool(value)
		c.DefaultIONOSConInsecure = v
	}

	if c.AutoscalerSecretPath == "" {
		if value, ok := os.LookupEnv(EnvIONOSSecretPath); ok {
			c.AutoscalerSecretPath = value
		} else {
			result = multierror.Append(result, errors.New("autoscaler secret path is not provided"))
		}
	}

	if c.ClusterID == "" {
		if value, ok := os.LookupEnv(EnvIONOSClusterID); ok {
			c.ClusterID = value
		} else {
			result = multierror.Append(errors.New("cluster id is not provided"))
		}

	}

	if c.PollTimeout == 0 {
		c.PollTimeout = DefaultTimeout
		if value, ok := os.LookupEnv(EnvIONOSPollTimeout); ok {
			t, err := time.ParseDuration(value)
			if err != nil {
				result = multierror.Append(errors.Wrapf(err, "error parsing poll timeout %s", value))
			} else {
				c.PollTimeout = t
			}
		}
	}

	if c.PollInterval == 0 {
		c.PollInterval = DefaultInterval
		if value, ok := os.LookupEnv(EnvIONOSPollInterval); ok {
			i, err := time.ParseDuration(value)
			if err != nil {
				result = multierror.Append(errors.Wrapf(err, "error parsing poll interval %s", value))
			} else {
				c.PollInterval = i
			}
		}
	}
	if c.PollInterval < time.Second {
		result = multierror.Append(errors.New("Poll interval should always be longer than a second"))
	}

	return result.ErrorOrNil()
}

var (
	createIECManager = CreateIECManager
)

func CreateIECManager(configReader io.Reader) (IECManager, error) {
	klog.V(debug).Info("Creating IEC manager")
	cfg := &Config{}
	if configReader != nil {
		klog.V(trace).Info("Decoding config yaml")
		err := yaml.NewDecoder(configReader).Decode(cfg)
		if err != nil {
			return nil, err
		}
	}

	err := cfg.valid()
	if err != nil {
		klog.Errorf("Error invalid config, %v", err)
		return nil, err
	}

	m := &IECManagerImpl{
		secretPath: cfg.AutoscalerSecretPath,
		clusterID:  cfg.ClusterID,
		nodeGroups: make([]*NodeGroup, 0),
		ionosConf: &clientConfObj{
			confPath:        cfg.AutoscalerSecretPath,
			defaultToken:    cfg.DefaultIONOSToken,
			insecure:        cfg.DefaultIONOSConInsecure,
			iecPollTimeout:  cfg.PollTimeout,
			iecPollInterval: cfg.PollInterval,
		},
		interrupt: make(chan struct{}),
	}

	go wait.Until(func() {
		for _, ng := range m.nodeGroups {
			ng.cache.reset()
		}
	}, time.Hour, m.interrupt)

	return m, nil
}

// Cleanup cleans up all resources before the cloud provider is removed
func (m IECManagerImpl) Cleanup() {
	close(m.interrupt)
}

func (m IECManagerImpl) processDC(dataCenterID string, iecConfig IECConfig) ([]*NodeGroup, error) {
	var (
		nodePools *profitbricks.KubernetesNodePools
		listError error
		groups    []*NodeGroup
	)
	if dataCenterID != "" {
		klog.V(debug).Infof("Working on datacenter %s.", dataCenterID)
	}
	var invalid error
	// For all tokens for datacenter
	for _, t := range iecConfig.Tokens {
		// Try to get a vaild token
		client := iecClientGetter(t, iecConfig.Endpoint, iecConfig.Insecure)
		nodePools, listError = client.ListKubernetesNodePools(m.clusterID)
		if listError != nil {
			// if unauthorized try next token
			if profitbricks.IsStatusUnauthorized(listError) {
				klog.V(trace).Infof("Token invalid, trying next one")
				invalid = listError
				continue
			}
			// any other error, work on next dc
			invalid = nil
			break
		}
		if nodePools != nil {
			invalid = nil
			break
		}
	}

	if invalid != nil {
		listError = fmt.Errorf("errors for all tokens: %+v", invalid)
	}
	if nodePools != nil {
		for _, nodePool := range nodePools.Items {
			klog.V(debug).Infof("Processing nodepool: %s", nodePool.ID)
			if nodePool.Properties.AutoScaling == nil || !nodePool.Properties.AutoScaling.Enabled() {
				klog.V(debug).Infof("Autoscaling for nodepool %s is disabled, skipping", nodePool.ID)
				continue
			}
			if dataCenterID != "" && nodePool.Properties.DatacenterID != dataCenterID {
				klog.V(debug).Infof(
					"Found nodepool in DC %s different from tokens dc %s, skipping", nodePool.Properties.DatacenterID, dataCenterID)
				continue
			}
			np := nodePool
			instanceCache := instanceCache{data: make(map[string]cloudprovider.Instance)}
			for _, ng := range m.nodeGroups {
				if ng.id == nodePool.ID {
					instanceCache.data = ng.cache.getData()
					break
				}
			}
			groups = append(groups, &NodeGroup{
				id:         nodePool.ID,
				clusterID:  m.clusterID,
				clientConf: m.ionosConf,
				nodePool:   &np,
				minSize:    int(*nodePool.Properties.AutoScaling.MinNodeCount),
				maxSize:    int(*nodePool.Properties.AutoScaling.MaxNodeCount),
				cache:      instanceCache,
			})
			klog.V(debug).Infof("Added group for node pool %q name: %s", nodePool.ID, nodePool.Properties.Name)
		}
	} else if listError != nil {
		klog.Errorf("error getting any nodegroup: %v", listError.Error())
	}
	return groups, listError
}

// Refreshes the cache holding the nodegroups. This is called by the CA based
// on the `--scan-interval`. By default it's 10 seconds.
func (m *IECManagerImpl) Refresh() error {
	klog.V(debug).Info("Refreshing")
	var res *multierror.Error
	var groups []*NodeGroup
	var groupIDs []string

	// Fixed behaviour, make only one request for all DCs and use the first working
	// token out of the list of all tokens from all DCs.
	iecConfig, err := m.ionosConf.getIECConfig("")
	if err != nil {
		return err
	}
	if m.ionosConf.defaultToken == "" {
		tokens := []string{}
		iecConfigs, err := configs(m.ionosConf.confPath)
		if err != nil {
			return err
		}
		for _, conf := range iecConfigs {
			tokens = append(tokens, conf.Tokens...)
		}
		iecConfig.Tokens = tokens
	}
	groups, err = m.processDC("", *iecConfig)
	res = multierror.Append(res, err)

	// Intended,  make one requests per DC and use DC's tokens for this request.
	//	if m.ionosConf.defaultToken != "" {
	//		// Default token set
	//		iecConfig, err := m.ionosConf.getIECConfig("")
	//		if err != nil {
	//			return err
	//		}
	//		groups, err = processDC("", m.clusterID, m.ionosConf, *iecConfig)
	//		res = multierror.Append(res, err)
	//	} else {
	//		iecConfigs, err := configs(m.ionosConf.confPath)
	//		if err != nil {
	//			return err
	//		}
	//
	//		// For all datacenters in config
	//		var dcGroups []*NodeGroup
	//		for dataCenterID, iecConfig := range iecConfigs {
	//			dcGroups, err = processDC(dataCenterID, m.clusterID, m.ionosConf, iecConfig)
	//			res = multierror.Append(res, err)
	//			groups = append(groups, dcGroups...)
	//		}
	//	}
	if len(groups) == 0 {
		klog.V(debug).Info("cluster-autoscaler is disabled. no node pools configured")
	}

	errOrNil := res.ErrorOrNil()
	if errOrNil == nil {
		for _, g := range groups {
			groupIDs = append(groupIDs, g.id)
		}
		klog.V(debug).Infof("Setting manager groups to %+v", groupIDs)
		m.nodeGroups = groups
	}
	return errOrNil
}

func (m *IECManagerImpl) GetNodeGroups() []*NodeGroup {
	return m.nodeGroups
}

func (m *IECManagerImpl) GetClusterID() string {
	return m.clusterID
}
