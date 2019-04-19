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

package opsservice

import (
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/storage/clusterconfig"

	"github.com/gravitational/trace"
	"github.com/pborman/uuid"
)

// CreateUpdateConfigOperation creates a new operation to update cluster configuration
func (o *Operator) CreateUpdateConfigOperation(req ops.CreateUpdateConfigOperationRequest) (*ops.SiteOperationKey, error) {
	err := req.ClusterKey.Check()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	cluster, err := o.openSite(req.ClusterKey)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	config, err := o.backend().GetGravityClusterConfig(req.ClusterKey.SiteDomain)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	configBytes, err := clusterconfig.Marshal(config)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	key, err := cluster.createUpdateConfigOperation(req, []byte(configBytes))
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return key, nil
}

// GetClusterConfiguration retrieves the cluster configuration
func (o *Operator) GetClusterConfiguration(key ops.SiteKey) (config clusterconfig.Interface, err error) {
	err = key.Check()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	config, err = o.backend().GetGravityClusterConfig(key.SiteDomain)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return config, nil
}

// UpdateClusterConfiguration updates the cluster configuration to the value given
// in the specified request
func (o *Operator) UpdateClusterConfiguration(req ops.UpdateClusterConfigRequest) error {
	err := req.ClusterKey.Check()
	if err != nil {
		return trace.Wrap(err)
	}
	existingConfig, err := o.GetClusterConfiguration(req.ClusterKey)
	if err != nil {
		return trace.Wrap(err)
	}
	configUpdate, err := clusterconfig.Unmarshal(req.Config)
	if err != nil {
		return trace.Wrap(err)
	}
	existingConfig.MergeFrom(configUpdate)
	err = o.backend().UpdateGravityClusterConfig(req.ClusterKey.SiteDomain, existingConfig)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// createUpdateConfigOperation creates a new operation to update cluster configuration
func (s *site) createUpdateConfigOperation(req ops.CreateUpdateConfigOperationRequest, prevConfig []byte) (*ops.SiteOperationKey, error) {
	op := ops.SiteOperation{
		ID:         uuid.New(),
		AccountID:  s.key.AccountID,
		SiteDomain: s.key.SiteDomain,
		Type:       ops.OperationUpdateConfig,
		Created:    s.clock().UtcNow(),
		Updated:    s.clock().UtcNow(),
		State:      ops.OperationUpdateConfigInProgress,
		UpdateConfig: &storage.UpdateConfigOperationState{
			PrevConfig: prevConfig,
			Config:     req.Config,
		},
	}
	key, err := s.getOperationGroup().createSiteOperation(op)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return key, nil
}
