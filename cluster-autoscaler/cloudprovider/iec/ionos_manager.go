package iec

import (
	"io"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
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

//go:generate mockery -name IECManager -inpkg -case snake -dir . -output . -testonly
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

type NodePool struct {
	// ID is the IONOS nodepool id
	ID string `yaml:"id"`
	// AutoscalingLimitMax is the upper limit for the autoscaler
	AutoscalingLimitMax int `yaml:"autoscaling_limit_max"`
	// AutoscalingLimitMin is the lower limit for the autoscaler
	AutoscalingLimitMin int `yaml:"autoscaling_limit_min"`
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

	// NodePools is a list of NodePools the autoscaler should work on
	NodePools []NodePool `yaml:"nodepools"`
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

	if c.AutoscalerSecretPath == "" && c.DefaultIONOSToken == "" {
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

func (m IECManagerImpl) initNodeGroups(nodePools []NodePool) []*NodeGroup {
	klog.V(info).Infof("Autoscaler operating on %d nodepools: %+v", len(nodePools), nodePools)
	nodeGroups := make([]*NodeGroup, len(nodePools))
	for i, np := range nodePools {
		nodeGroup := NodeGroup{
			id:                 np.ID,
			clusterID:          m.clusterID,
			clientConf:         m.ionosConf,
			minSize:            np.AutoscalingLimitMin,
			maxSize:            np.AutoscalingLimitMax,
			cache:              instanceCache{data: map[string]cloudprovider.Instance{}},
			deletionInProgress: wipLock{},
		}
		nodeGroups[i] = &nodeGroup
		klog.V(trace).Infof("Created from nodepool %s: %+v", np.ID, nodeGroup)
	}
	return nodeGroups
}

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
		ionosConf: &clientConfObj{
			confPath:        cfg.AutoscalerSecretPath,
			defaultToken:    cfg.DefaultIONOSToken,
			insecure:        cfg.DefaultIONOSConInsecure,
			iecPollTimeout:  cfg.PollTimeout,
			iecPollInterval: cfg.PollInterval,
		},
		interrupt: make(chan struct{}),
	}

	m.nodeGroups = m.initNodeGroups(cfg.NodePools)

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

// Refreshes the cache holding the nodegroups. This is called by the CA based
// on the `--scan-interval`. By default it's 10 seconds.
func (m *IECManagerImpl) Refresh() error {
	return nil
}

func (m *IECManagerImpl) GetNodeGroups() []*NodeGroup {
	return m.nodeGroups
}

func (m *IECManagerImpl) GetClusterID() string {
	return m.clusterID
}
