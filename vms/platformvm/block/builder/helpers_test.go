// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package builder

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/codec/linearcodec"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/memdb"
	"github.com/ava-labs/avalanchego/database/versiondb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/uptime"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/config"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/metrics"
	"github.com/ava-labs/avalanchego/vms/platformvm/network"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/mempool"
	"github.com/ava-labs/avalanchego/vms/platformvm/utxo"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"

	blockexecutor "github.com/ava-labs/avalanchego/vms/platformvm/block/executor"
	ts "github.com/ava-labs/avalanchego/vms/platformvm/testsetup"
	txbuilder "github.com/ava-labs/avalanchego/vms/platformvm/txs/builder"
	txexecutor "github.com/ava-labs/avalanchego/vms/platformvm/txs/executor"
	pvalidators "github.com/ava-labs/avalanchego/vms/platformvm/validators"
)

var testSubnet1 *txs.Tx

type environment struct {
	Builder
	blkManager blockexecutor.Manager
	mempool    mempool.Mempool
	network    network.Network
	sender     *common.SenderTest

	isBootstrapped *utils.Atomic[bool]
	config         *config.Config
	clk            *mockable.Clock
	baseDB         *versiondb.Database
	ctx            *snow.Context
	msm            *ts.MutableSharedMemory
	fx             fx.Fx
	state          state.State
	atomicUTXOs    avax.AtomicUTXOManager
	uptimes        uptime.Manager
	utxosHandler   utxo.Handler
	txBuilder      txbuilder.Builder
	backend        txexecutor.Backend
}

func newEnvironment(t *testing.T) *environment {
	r := require.New(t)

	var (
		fork     = ts.DurangoFork
		forkTime = ts.ValidateStartTime.Add(-2 * time.Second)
	)

	res := &environment{
		isBootstrapped: &utils.Atomic[bool]{},
		config:         ts.Config(fork, forkTime),
		clk:            defaultClock(),
	}
	res.isBootstrapped.Set(true)

	res.baseDB = versiondb.New(memdb.New())
	res.ctx, res.msm = ts.Context(t, res.baseDB)

	res.ctx.Lock.Lock()
	defer res.ctx.Lock.Unlock()

	res.fx = defaultFx(t, res.clk, res.ctx.Log, res.isBootstrapped.Get())

	rewardsCalc := reward.NewCalculator(res.config.RewardConfig)
	res.state = defaultState(t, res.config, res.ctx, res.baseDB, rewardsCalc)

	res.atomicUTXOs = avax.NewAtomicUTXOManager(res.ctx.SharedMemory, txs.Codec)
	res.uptimes = uptime.NewManager(res.state, res.clk)
	res.utxosHandler = utxo.NewHandler(res.ctx, res.clk, res.fx)

	res.txBuilder = txbuilder.New(
		res.ctx,
		res.config,
		res.clk,
		res.fx,
		res.state,
		res.atomicUTXOs,
		res.utxosHandler,
	)

	genesisID := res.state.GetLastAccepted()
	res.backend = txexecutor.Backend{
		Config:       res.config,
		Ctx:          res.ctx,
		Clk:          res.clk,
		Bootstrapped: res.isBootstrapped,
		Fx:           res.fx,
		FlowChecker:  res.utxosHandler,
		Uptimes:      res.uptimes,
		Rewards:      rewardsCalc,
	}

	registerer := prometheus.NewRegistry()
	res.sender = &common.SenderTest{T: t}
	res.sender.SendAppGossipF = func(context.Context, []byte) error {
		return nil
	}

	metrics, err := metrics.New("", registerer)
	r.NoError(err)

	res.mempool, err = mempool.New("mempool", registerer, nil)
	r.NoError(err)

	res.blkManager = blockexecutor.NewManager(
		res.mempool,
		metrics,
		res.state,
		&res.backend,
		pvalidators.TestManager,
	)

	txVerifier := network.NewLockedTxVerifier(&res.ctx.Lock, res.blkManager)
	res.network, err = network.New(
		res.backend.Ctx.Log,
		res.backend.Ctx.NodeID,
		res.backend.Ctx.SubnetID,
		res.backend.Ctx.ValidatorState,
		txVerifier,
		res.mempool,
		res.backend.Config.PartialSyncPrimaryNetwork,
		res.sender,
		registerer,
		network.DefaultConfig,
	)
	r.NoError(err)

	res.Builder = New(
		res.mempool,
		res.txBuilder,
		&res.backend,
		res.blkManager,
	)
	res.Builder.StartBlockTimer()

	res.blkManager.SetPreference(genesisID)
	addSubnet(t, res)

	return res
}

func addSubnet(t *testing.T, env *environment) {
	require := require.New(t)

	// Create a subnet
	var err error
	testSubnet1, err = env.txBuilder.NewCreateSubnetTx(
		2, // threshold; 2 sigs from keys[0], keys[1], keys[2] needed to add validator to this subnet
		[]ids.ShortID{ // control keys
			ts.SubnetControlKeys[0].PublicKey().Address(),
			ts.SubnetControlKeys[1].PublicKey().Address(),
			ts.SubnetControlKeys[2].PublicKey().Address(),
		},
		[]*secp256k1.PrivateKey{ts.Keys[4]},
		ts.Keys[0].PublicKey().Address(),
	)
	require.NoError(err)

	// store it
	genesisID := env.state.GetLastAccepted()
	stateDiff, err := state.NewDiff(genesisID, env.blkManager)
	require.NoError(err)

	executor := txexecutor.StandardTxExecutor{
		Backend: &env.backend,
		State:   stateDiff,
		Tx:      testSubnet1,
	}
	require.NoError(testSubnet1.Unsigned.Visit(&executor))

	stateDiff.AddTx(testSubnet1, status.Committed)
	require.NoError(stateDiff.Apply(env.state))
}

func defaultState(
	t *testing.T,
	cfg *config.Config,
	ctx *snow.Context,
	db database.Database,
	rewards reward.Calculator,
) state.State {
	require := require.New(t)

	execCfg, _ := config.GetExecutionConfig([]byte(`{}`))
	genesis, err := ts.BuildGenesis(ctx)
	require.NoError(err)
	state, err := state.New(
		db,
		genesis,
		prometheus.NewRegistry(),
		cfg,
		execCfg,
		ctx,
		metrics.Noop,
		rewards,
	)
	require.NoError(err)

	// persist and reload to init a bunch of in-memory stuff
	state.SetHeight(0)
	require.NoError(state.Commit())
	return state
}

func defaultClock() *mockable.Clock {
	// set time after Banff fork (and before default nextStakerTime)
	clk := &mockable.Clock{}
	clk.Set(ts.GenesisTime)
	return clk
}

type fxVMInt struct {
	registry codec.Registry
	clk      *mockable.Clock
	log      logging.Logger
}

func (fvi *fxVMInt) CodecRegistry() codec.Registry {
	return fvi.registry
}

func (fvi *fxVMInt) Clock() *mockable.Clock {
	return fvi.clk
}

func (fvi *fxVMInt) Logger() logging.Logger {
	return fvi.log
}

func defaultFx(t *testing.T, clk *mockable.Clock, log logging.Logger, isBootstrapped bool) fx.Fx {
	require := require.New(t)

	fxVMInt := &fxVMInt{
		registry: linearcodec.NewDefault(),
		clk:      clk,
		log:      log,
	}
	res := &secp256k1fx.Fx{}
	require.NoError(res.Initialize(fxVMInt))
	if isBootstrapped {
		require.NoError(res.Bootstrapped())
	}
	return res
}

func shutdownEnvironment(env *environment) error {
	env.Builder.ShutdownBlockTimer()

	if env.isBootstrapped.Get() {
		validatorIDs := env.config.Validators.GetValidatorIDs(constants.PrimaryNetworkID)

		if err := env.uptimes.StopTracking(validatorIDs, constants.PrimaryNetworkID); err != nil {
			return err
		}
		if err := env.state.Commit(); err != nil {
			return err
		}
	}

	return utils.Err(
		env.state.Close(),
		env.baseDB.Close(),
	)
}
