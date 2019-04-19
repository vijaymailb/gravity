/*
Copyright 2019 Gravitational, Inc.

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

package clusterconfig

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/utils"

	teleservices "github.com/gravitational/teleport/lib/services"
	teleutils "github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
)

// Interface manages cluster configuration
type Interface interface {
	// Resource provides common resource methods
	teleservices.Resource
	// GetKubeletConfig returns the configuration of the kubelet
	GetKubeletConfig() *Kubelet
	// GetGlobalConfig returns the global configuration
	GetGlobalConfig() Global
	// MergeFrom merges configuration from other
	MergeFrom(other Interface)
}

// New returns a new instance of the resource initialized to specified spec
func New(spec Spec) (*Resource, error) {
	if err := spec.checkAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}
	res := newEmpty()
	res.Spec = spec
	return res, nil
}

// NewEmpty returns a new instance of the resource initialized to defaults
func NewEmpty() *Resource {
	res := newEmpty()
	return res
}

// Resource describes the cluster configuration resource
type Resource struct {
	// Kind is the resource kind
	Kind string `json:"kind"`
	// Version is the resource version
	Version string `json:"version"`
	// Metadata specifies resource metadata
	Metadata teleservices.Metadata `json:"metadata"`
	// Spec defines the resource
	Spec Spec `json:"spec"`
}

// GetName returns the name of the resource name
func (r *Resource) GetName() string {
	return r.Metadata.Name
}

// SetName resets the resource name to the specified value
func (r *Resource) SetName(name string) {
	r.Metadata.Name = name
}

// GetMetadata returns resource metadata
func (r *Resource) GetMetadata() teleservices.Metadata {
	return r.Metadata
}

// SetExpiry resets expiration time to the specified value
func (r *Resource) SetExpiry(expires time.Time) {
	r.Metadata.SetExpiry(expires)
}

// Expires returns expiration time
func (r *Resource) Expiry() time.Time {
	return r.Metadata.Expiry()
}

// SetTTL resets the resources's time to live to the specified value
// using given clock implementation
func (r *Resource) SetTTL(clock clockwork.Clock, ttl time.Duration) {
	r.Metadata.SetTTL(clock, ttl)
}

// GetKubeletConfig returns the configuration of the kubelet
func (r *Resource) GetKubeletConfig() *Kubelet {
	return r.Spec.ComponentConfigs.Kubelet
}

// GetGlobalConfig returns the global configuration
func (r *Resource) GetGlobalConfig() Global {
	return r.Spec.Global
}

// MergeFrom merges configuration from other
func (r *Resource) MergeFrom(other Interface) {
	globalConfig := other.GetGlobalConfig()
	if globalConfig.ServiceCIDR != "" {
		r.Spec.Global.ServiceCIDR = globalConfig.ServiceCIDR
	}
	if globalConfig.PodCIDR != "" {
		r.Spec.Global.PodCIDR = globalConfig.PodCIDR
	}
	if globalConfig.PodCIDR != "" {
		r.Spec.Global.PodCIDR = globalConfig.PodCIDR
	}
	if globalConfig.CloudConfig != "" {
		r.Spec.Global.CloudConfig = globalConfig.CloudConfig
	}
	if globalConfig.ProxyPortRange != "" {
		r.Spec.Global.ProxyPortRange = globalConfig.ProxyPortRange
	}
	if globalConfig.ServiceNodePortRange != "" {
		r.Spec.Global.ServiceNodePortRange = globalConfig.ServiceNodePortRange
	}
	if len(globalConfig.FeatureGates) != 0 {
		for k, v := range globalConfig.FeatureGates {
			r.Spec.Global.FeatureGates[k] = v
		}
	}
	kubeletConfig := other.GetKubeletConfig()
	if kubeletConfig == nil {
		return
	}
	if r.Spec.ComponentConfigs.Kubelet == nil {
		r.Spec.ComponentConfigs.Kubelet = &Kubelet{}
	}
	if len(kubeletConfig.ExtraArgs) != 0 {
		r.Spec.ComponentConfigs.Kubelet.ExtraArgs = kubeletConfig.ExtraArgs
	}
	if len(kubeletConfig.Config) != 0 {
		r.Spec.ComponentConfigs.Kubelet.Config = kubeletConfig.Config
	}
}

// Unmarshal unmarshals the resource from either YAML- or JSON-encoded data
func Unmarshal(data []byte) (*Resource, error) {
	if len(data) == 0 {
		return nil, trace.BadParameter("empty input")
	}
	jsonData, err := teleutils.ToJSON(data)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	var hdr teleservices.ResourceHeader
	err = json.Unmarshal(jsonData, &hdr)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	switch hdr.Version {
	case "v1":
		var config Resource
		err := teleutils.UnmarshalWithSchema(getSpecSchema(), &config, jsonData)
		if err != nil {
			return nil, trace.BadParameter(err.Error())
		}
		// TODO(dmitri): set namespace explicitly - schema default is ignored
		// as teleservices.Metadata.Namespace is configured as unserializable
		config.Metadata.Namespace = defaults.KubeSystemNamespace
		config.Metadata.Name = constants.ClusterConfigurationMap
		if config.Metadata.Expires != nil {
			teleutils.UTC(config.Metadata.Expires)
		}
		return &config, nil
	}
	return nil, trace.BadParameter(
		"%v resource version %q is not supported", Kind, hdr.Version)
}

// Marshal marshals this resource as JSON
func Marshal(config Interface, opts ...teleservices.MarshalOption) ([]byte, error) {
	return json.Marshal(config)
}

// checkAndSetDefaults validates this object
func (r *Spec) checkAndSetDefaults() error {
	if r.Global.CloudProvider == "" {
		r.Global.CloudProvider = schema.ProviderGeneric
	}
	if err := schema.ValidateCloudProvider(r.Global.CloudProvider); err != nil {
		return trace.Wrap(err)
	}
	// Default service/pod CIDRs
	if r.Global.PodCIDR == "" {
		r.Global.PodCIDR = defaults.PodSubnet
	}
	if r.Global.ServiceCIDR == "" {
		r.Global.ServiceCIDR = defaults.ServiceSubnet
	}
	if r.Global.CloudProvider == schema.ProviderAWS {
		// FIXME(dmitri): is this accurate?
		// I could not find where service CIDR might be set to anything other than the default
		// on AWS
		subnet := defaults.AWSVPCCIDR
		// select the overlay subnet non-overlapping with the machines subnet
		overlay, err := utils.SelectSubnet([]string{subnet})
		if err != nil {
			return trace.Wrap(err)
		}
		// and select the service subnet non-overlapping with the pod/machine subnets
		service, err := utils.SelectSubnet([]string{subnet, overlay})
		if err != nil {
			return trace.Wrap(err)
		}
		r.Global.PodCIDR = overlay
		r.Global.ServiceCIDR = service
	}
	err := utils.ValidateKubernetesSubnets(r.Global.PodCIDR, r.Global.ServiceCIDR)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// Spec defines the cluster configuration resource
type Spec struct {
	// ComponentsConfigs groups component configurations
	ComponentConfigs
	// TODO: Scheduler, ControllerManager, Proxy
	// Global describes global configuration
	Global Global `json:"global"`
}

// ComponentsConfigs groups component configurations
type ComponentConfigs struct {
	// Kubelet defines kubelet configuration
	Kubelet *Kubelet `json:"kubelet,omitempty"`
}

// Kubelet defines kubelet configuration
type Kubelet struct {
	// ExtraArgs lists additional command line arguments
	ExtraArgs []string `json:"extraArgs,omitempty"`
	// Config defines the kubelet configuration as a JSON-formatted
	// payload
	Config json.RawMessage `json:"config,omitempty"`
}

// ControlPlaneComponent defines configuration of a control plane component
type ControlPlaneComponent struct {
	json.RawMessage
}

// Global describes global configuration
type Global struct {
	// CloudProvider specifies the cloud provider
	CloudProvider string `json:"cloudProvider,omitempty"`
	// CloudConfig describes the cloud configuration.
	// The configuration is provider-specific
	CloudConfig string `json:"cloudConfig,omitempty"`
	// ServiceCIDR represents the IP range from which to assign service cluster IPs.
	// This must not overlap with any IP ranges assigned to nodes for pods.
	// Targets: api server, controller manager
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	// ServiceNodePortRange defines the range of ports to reserve for services with NodePort visibility.
	// Inclusive at both ends of the range.
	// Targets: api server
	ServiceNodePortRange string `json:"serviceNodePortRange,omitempty"`
	// PodCIDR defines the CIDR Range for Pods in cluster.
	// Targets: controller manager, kubelet
	PodCIDR string `json:"podCIDR,omitempty"`
	// ProxyPortRange specifies the range of host ports (beginPort-endPort, single port or beginPort+offset, inclusive)
	// that may be consumed in order to proxy service traffic.
	// If (unspecified, 0, or 0-0) then ports will be randomly chosen.
	// Targets: kube-proxy
	ProxyPortRange string `json:"proxyPortRange,omitempty"`
	// FeatureGates defines the set of key=value pairs that describe feature gates for alpha/experimental features.
	// Targets: all components
	FeatureGates map[string]bool `json:"featureGates,omitempty"`
}

// Kind defines the resource kind for cluster configuration
const Kind = "clusterconfiguration"

// specSchemaTemplate is JSON schema for the cluster configuration resource
const specSchemaTemplate = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["kind", "spec", "version"],
  "properties": {
    "kind": {"type": "string"},
    "version": {"type": "string", "default": "v1"},
    "metadata": {
      "default": {},
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "name": {"type": "string", "default": "%v"},
        "namespace": {"type": "string", "default": "%v"},
        "description": {"type": "string"},
        "expires": {"type": "string"},
        "labels": {
          "type": "object",
          "patternProperties": {
             "^[a-zA-Z/.0-9_-]$":  {"type": "string"}
          }
        }
      }
    },
    "spec": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "global": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "cloudProvider": {"type": "string"},
            "cloudConfig": {"type": "string"},
            "serviceCIDR": {"type": "string"},
            "serviceNodePortRange": {"type": "string"},
            "poxyPortRange": {"type": "string"},
            "podCIDR": {"type": "string"},
            "featureGates": {
              "type": "object",
              "patternProperties": {
                 "^[a-zA-Z]+[a-zA-Z0-9]*$": {"type": "boolean"}
              }
            }
          }
        },
        "kubelet": {
          "type": "object",
          "additionalProperties": false,
          "required": ["config"],
          "properties": {
            "config": {
              "type": "object",
              "properties": {
                "kind": {"type": "string"},
                "apiVersion": {"type": "string"},
                "staticPodPath": {"type": "string"},
                "syncFrequency": {"type": "string"},
                "fileCheckFrequency": {"type": "string"},
                "httpCheckFrequency": {"type": "string"},
                "address": {"type": "string"},
                "port": {"type": "integer"},
                "readOnlyPort": {"type": "integer"},
                "tlsCertFile": {"type": "string"},
                "tlsPrivateKeyFile": {"type": "string"},
                "tlsCipherSuites": {"type": "array", "items": {"type": "string"}},
                "tlsMinVersion": {"type": "string"},
                "rotateCertificates": {"type": ["string", "boolean"]},
                "serverTLSBootstrap": {"type": ["string", "boolean"]},
                "authentication": {"type": "object"},
                "authorization": {"type": "object"},
                "registryPullQPS": {"type": ["null", "integer"]},
                "registryBurst": {"type": "integer"},
                "eventRecordQPS": {"type": ["null", "integer"]},
                "eventBurst": {"type": "integer"},
                "enableDebuggingHandlers": {"type": ["string", "boolean"]},
                "enableContentionProfiling": {"type": ["string", "boolean"]},
                "healthzPort": {"type": ["null", "integer"]},
                "healthzBindAddress": {"type": "string"},
                "oomScoreAdj": {"type": ["null", "integer"]},
                "clusterDomain": {"type": "string"},
                "clusterDNS": {"type": "array", "items": {"type": "string"}},
                "streamingConnectionIdleTimeout": {"type": "string"},
                "nodeStatusUpdateFrequency": {"type": "string"},
                "nodeStatusReportFrequency": {"type": "string"},
                "nodeLeaseDurationSeconds": {"type": "integer"},
                "imageMinimumGCAge": {"type": "string"},
                "imageGCHighThresholdPercent": {"type": ["null", "integer"]},
                "imageGCLowThresholdPercent": {"type": ["null", "integer"]},
                "volumeStatsAggPeriod": {"type": "string"},
                "kubeletCgroups": {"type": "string"},
                "systemCgroups": {"type": "string"},
                "cgroupRoot": {"type": "string"},
                "cgroupsPerQOS": {"type": ["string", "boolean"]},
                "cgroupDriver": {"type": "string"},
                "cpuManagerPolicy": {"type": "string"},
                "cpuManagerReconcilePeriod": {"type": "string"},
                "qosReserved": {"type": "object"},
                "runtimeRequestTimeout": {"type": "string"},
                "hairpinMode": {"type": "string"},
                "maxPods": {"type": "integer"},
                "podCIDR": {"type": "string"},
                "podPidsLimit": {"type": ["null", "integer"]},
                "resolvConf": {"type": "string"},
                "cpuCFSQuota": {"type": ["string", "boolean"]},
                "cpuCFSQuotaPeriod": {"type": "string"},
                "maxOpenFiles": {"type": "integer"},
                "contentType": {"type": "string"},
                "kubeAPIQPS": {"type": ["null", "integer"]},
                "kubeAPIBurst": {"type": "integer"},
                "serializeImagePulls": {"type": ["string", "boolean"]},
                "evictionHard": {"type": "object"},
                "evictionSoft": {"type": "object"},
                "evictionSoftGracePeriod": {"type": "object"},
                "evictionPressureTransitionPeriod": {"type": "string"},
                "evictionMaxPodGracePeriod": {"type": "integer"},
                "evictionMinimumReclaim": {"type": "object"},
                "podsPerCore": {"type": "integer"},
                "enableControllerAttachDetach": {"type": "boolean"},
                "protectKernelDefaults": {"type": "boolean"},
                "makeIPTablesUtilChains": {"type": "boolean"},
                "iptablesMasqueradeBit": {"type": ["null", "integer"]},
                "iptablesDropBit": {"type": ["null", "integer"]},
                "featureGates": {
                  "type": "object",
                  "patternProperties": {
                     "^[a-zA-Z]+[a-zA-Z0-9]*$": {"type": "boolean"}
                  }
                },
                "failSwapOn": {"type": "boolean"},
                "containerLogMaxSize": {"type": "string"},
                "containerLogMaxFiles": {"type": ["null", "integer"]},
                "configMapAndSecretChangeDetectionStrategy": {"type": "object"},
                "systemReserved": {"type": "object"},
                "kubeReserved": {"type": "object"},
                "systemReservedCgroup": {"type": "string"},
                "kubeReservedCgroup": {"type": "string"},
                "enforceNodeAllocatable": {"type": "array", "items": {"type": "string"}}
              }
            },
            "extraArgs": {"type": "array", "items": {"type": "string"}}
          }
        }
      }
    }
  }
}`

// getSpecSchema returns the formatted JSON schema for the cluster configuration resource
func getSpecSchema() string {
	return fmt.Sprintf(specSchemaTemplate,
		constants.ClusterConfigurationMap, defaults.KubeSystemNamespace)
}

func newEmpty() *Resource {
	return &Resource{
		Kind:    Kind,
		Version: "v1",
		Metadata: teleservices.Metadata{
			Name:      constants.ClusterConfigurationMap,
			Namespace: defaults.KubeSystemNamespace,
		},
	}
}
