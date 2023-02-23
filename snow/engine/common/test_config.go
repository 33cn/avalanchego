// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package common

import (
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common/tracker"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/subnets"
)

// DefaultConfigTest returns a test configuration
func DefaultConfigTest() Config {
	isSynced := false
	subnetStateTracker := &subnets.SyncTrackerTest{
		IsSyncedF: func() bool {
			return isSynced
		},
		BootstrappedF: func(ids.ID) {
			isSynced = true
		},
	}

	beacons := validators.NewSet()

	connectedPeers := tracker.NewPeers()
	startupTracker := tracker.NewStartup(connectedPeers, 0)
	beacons.RegisterCallbackListener(startupTracker)

	return Config{
		Ctx:                            snow.DefaultConsensusContextTest(),
		Beacons:                        beacons,
		StartupTracker:                 startupTracker,
		Sender:                         &SenderTest{},
		Bootstrapable:                  &BootstrapableTest{},
		SubnetStateTracker:             subnetStateTracker,
		Timer:                          &TimerTest{},
		AncestorsMaxContainersSent:     2000,
		AncestorsMaxContainersReceived: 2000,
		SharedCfg:                      &SharedConfig{},
	}
}
