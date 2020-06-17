package iec

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"k8s.io/klog"

	"github.com/profitbricks/profitbricks-sdk-go/v5"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
)

const (
	iecProviderIDPrefix = "ionos://"
	iecErrorCode        = "no-code-iec"
)

// toProviderID converts plain node id to a node.spec.ProviderID
func toProviderID(nodeID string) string {
	return fmt.Sprintf("%s%s", iecProviderIDPrefix, nodeID)
}

// toNodeID converts a node.spec.ProviderId to plain node id
func toNodeID(providerID string) string {
	return strings.TrimPrefix(providerID, iecProviderIDPrefix)
}

// toInstances converts a slice of *korev2.NodeResult to an array of cloudprovider.Instance
func toInstances(nodes *profitbricks.KubernetesNodes) []cloudprovider.Instance {
	instances := make([]cloudprovider.Instance, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		instances = append(instances, toInstance(node))
	}
	return instances
}

// to Instance converts a given *korev2.NodeResult to a cloudprovider.Instance
func toInstance(node profitbricks.KubernetesNode) cloudprovider.Instance {
	return cloudprovider.Instance{
		Id:     toProviderID(node.ID),
		Status: toInstanceStatus(node.Metadata.State),
	}
}

// toInstanceStatus converts the given profitbricks node state to a cloudprovider.InstanceStatus
func toInstanceStatus(nodeState string) *cloudprovider.InstanceStatus {
	st := &cloudprovider.InstanceStatus{}
	switch nodeState {
	case profitbricks.K8sNodeStateProvisioning, profitbricks.K8sNodeStateProvisioned, profitbricks.K8sNodeStateRebuilding:
		st.State = cloudprovider.InstanceCreating
	case profitbricks.K8sNodeStateTerminating:
		st.State = cloudprovider.InstanceDeleting
	case profitbricks.K8sNodeStateReady:
		st.State = cloudprovider.InstanceRunning
	default:
		st.ErrorInfo = &cloudprovider.InstanceErrorInfo{
			ErrorClass:   cloudprovider.OtherErrorClass,
			ErrorCode:    iecErrorCode,
			ErrorMessage: fmt.Sprintf("Unknown node state: %s", nodeState),
		}
	}
	return st
}

type IECConfig struct {
	Tokens   []string `json:"tokens"`
	Endpoint string   `json:"endpoint"`
	Insecure bool     `json:"insecure"`
}

var read = readFile

func readFile(file string) ([]byte, error) {
	return ioutil.ReadFile(file)
}

var files = listFiles

func listFiles(path string) ([]string, error) {
	confFiles, err := filepath.Glob(filepath.Join(path, "[a-zA-Z0-9\\-]*"))
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("Found %d cloud configs %v", len(confFiles), confFiles)
	return confFiles, nil
}

var configs = getIECConfigs

func getIECConfigs(confPath string) (map[string]IECConfig, error) {
	var confMap = map[string]IECConfig{}
	cloudConfs, err := files(confPath)
	if err != nil {
		return nil, fmt.Errorf("error getting files from %s: %v", confPath, err)
	}
	for _, f := range cloudConfs {
		conf, err := read(f)
		if err != nil {
			return nil, fmt.Errorf("couldn't read ionos iecClient config file %s, %v", f, err)
		}
		var cloudConfig IECConfig
		err = json.Unmarshal(conf, &cloudConfig)
		dcName := filepath.Base(f)
		klog.V(4).Infof("Cloud config found for DC: %s", dcName)
		confMap[dcName] = cloudConfig

		if err != nil {
			return nil, fmt.Errorf("error unmarshaling cloud config %s, %v", f, err)
		}
	}
	return confMap, nil
}

func (o *clientConfObj) getIECConfig(datacenterID string) (*IECConfig, error) {
	// If default token is set use it.
	if o.defaultToken != "" {
		return &IECConfig{
			Tokens:   []string{o.defaultToken},
			Endpoint: o.defaultEndpoint,
			Insecure: o.insecure,
		}, nil
	}

	confMap, err := configs(o.confPath)
	if err != nil {
		return nil, err
	}

	if datacenterID == "" {
		for k := range confMap {
			datacenterID = k
			break
		}
	}
	klog.V(4).Infof("Using datacenterID: %s", datacenterID)
	if config, ok := confMap[datacenterID]; ok {
		return &config, nil
	} else {
		klog.Errorf("couldn't find config data for datacenter id: %s", datacenterID)
		return nil, fmt.Errorf("couldn't find config data for datacenter id: %s", datacenterID)
	}
}
