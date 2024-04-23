// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package snowman

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/cache"
	"github.com/ava-labs/avalanchego/cache/metercacher"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/proto/pb/p2p"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman/poll"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/common/tracker"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/ancestor"
	"github.com/ava-labs/avalanchego/snow/event"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/utils/bag"
	"github.com/ava-labs/avalanchego/utils/bimap"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/utils/wrappers"
)

const nonVerifiedCacheSize = 64 * units.MiB

var _ common.Engine = (*Transitive)(nil)

func cachedBlockSize(_ ids.ID, blk snowman.Block) int {
	return ids.IDLen + len(blk.Bytes()) + constants.PointerOverhead
}

// Transitive implements the Engine interface by attempting to fetch all
// Transitive dependencies.
type Transitive struct {
	Config
	*metrics

	// list of NoOpsHandler for messages dropped by engine
	common.StateSummaryFrontierHandler
	common.AcceptedStateSummaryHandler
	common.AcceptedFrontierHandler
	common.AcceptedHandler
	common.AncestorsHandler
	common.AppHandler
	validators.Connector

	requestID uint32

	// track outstanding preference requests
	polls poll.Set

	// blocks that have we have sent get requests for but haven't yet received
	blkReqs            *bimap.BiMap[common.Request, ids.ID]
	blkReqSourceMetric map[common.Request]prometheus.Counter

	// blocks that are queued to be issued to consensus once missing dependencies are fetched
	// Block ID --> Block
	pending map[ids.ID]snowman.Block

	// Block ID --> Parent ID
	nonVerifieds ancestor.Tree

	// Block ID --> Block.
	// A block is put into this cache if it was not able to be issued. A block
	// fails to be issued if verification on the block or one of its ancestors
	// occurs.
	nonVerifiedCache cache.Cacher[ids.ID, snowman.Block]

	// acceptedFrontiers of the other validators of this chain
	acceptedFrontiers tracker.Accepted

	// operations that are blocked on a block being issued. This could be
	// issuing another block, responding to a query, or applying votes to consensus
	blocked event.Blocker

	// number of times build block needs to be called once the number of
	// processing blocks has gone below the optimal number.
	pendingBuildBlocks int

	// errs tracks if an error has occurred in a callback
	errs wrappers.Errs
}

func New(config Config) (*Transitive, error) {
	config.Ctx.Log.Info("initializing consensus engine")

	nonVerifiedCache, err := metercacher.New[ids.ID, snowman.Block](
		"non_verified_cache",
		config.Ctx.Registerer,
		cache.NewSizedLRU[ids.ID, snowman.Block](
			nonVerifiedCacheSize,
			cachedBlockSize,
		),
	)
	if err != nil {
		return nil, err
	}

	acceptedFrontiers := tracker.NewAccepted()
	config.Validators.RegisterSetCallbackListener(config.Ctx.SubnetID, acceptedFrontiers)

	factory := poll.NewEarlyTermNoTraversalFactory(
		config.Params.AlphaPreference,
		config.Params.AlphaConfidence,
	)
	polls, err := poll.NewSet(
		factory,
		config.Ctx.Log,
		"",
		config.Ctx.Registerer,
	)
	if err != nil {
		return nil, err
	}

	metrics, err := newMetrics("", config.Ctx.Registerer)
	if err != nil {
		return nil, err
	}

	return &Transitive{
		Config:                      config,
		metrics:                     metrics,
		StateSummaryFrontierHandler: common.NewNoOpStateSummaryFrontierHandler(config.Ctx.Log),
		AcceptedStateSummaryHandler: common.NewNoOpAcceptedStateSummaryHandler(config.Ctx.Log),
		AcceptedFrontierHandler:     common.NewNoOpAcceptedFrontierHandler(config.Ctx.Log),
		AcceptedHandler:             common.NewNoOpAcceptedHandler(config.Ctx.Log),
		AncestorsHandler:            common.NewNoOpAncestorsHandler(config.Ctx.Log),
		AppHandler:                  config.VM,
		Connector:                   config.VM,
		pending:                     make(map[ids.ID]snowman.Block),
		nonVerifieds:                ancestor.NewTree(),
		nonVerifiedCache:            nonVerifiedCache,
		acceptedFrontiers:           acceptedFrontiers,
		polls:                       polls,
		blkReqs:                     bimap.New[common.Request, ids.ID](),
		blkReqSourceMetric:          make(map[common.Request]prometheus.Counter),
	}, nil
}

func (t *Transitive) Gossip(ctx context.Context) error {
	if numProcessing := t.Consensus.NumProcessing(); numProcessing != 0 {
		t.Ctx.Log.Debug("skipping block gossip",
			zap.String("reason", "blocks currently processing"),
			zap.Int("numProcessing", numProcessing),
		)

		// executeDeferredWork is called here to unblock the engine if it
		// previously errored when attempting to issue a query. This can happen
		// if a subnet was temporarily misconfigured and there were no
		// validators.
		return t.executeDeferredWork(ctx)
	}

	t.Ctx.Log.Verbo("sampling from validators",
		zap.Stringer("validators", t.Validators),
	)

	// Uniform sampling is used here to reduce bandwidth requirements of
	// nodes with a large amount of stake weight.
	vdrID, ok := t.ConnectedValidators.SampleValidator()
	if !ok {
		t.Ctx.Log.Warn("skipping block gossip",
			zap.String("reason", "no connected validators"),
		)
		return nil
	}

	lastAcceptedID, lastAcceptedHeight := t.Consensus.LastAccepted()
	nextHeightToAccept, err := math.Add64(lastAcceptedHeight, 1)
	if err != nil {
		t.Ctx.Log.Error("skipping block gossip",
			zap.String("reason", "block height overflow"),
			zap.Stringer("blkID", lastAcceptedID),
			zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
			zap.Error(err),
		)
		return nil
	}

	t.requestID++
	t.Sender.SendPullQuery(
		ctx,
		set.Of(vdrID),
		t.requestID,
		t.Consensus.Preference(),
		nextHeightToAccept,
	)
	return nil
}

func (t *Transitive) Put(ctx context.Context, nodeID ids.NodeID, requestID uint32, blkBytes []byte) error {
	request := common.Request{
		NodeID:    nodeID,
		RequestID: requestID,
	}
	expectedBlkID, ok := t.blkReqs.DeleteKey(request)
	if !ok {
		t.Ctx.Log.Debug("unexpected Put",
			zap.Stringer("nodeID", nodeID),
			zap.Uint32("requestID", requestID),
		)
		t.metrics.numUselessPutBytes.Add(float64(len(blkBytes)))
		return nil
	}
	issuedMetric := t.blkReqSourceMetric[request]
	delete(t.blkReqSourceMetric, request)

	blk, err := t.VM.ParseBlock(ctx, blkBytes)
	if err != nil {
		t.Ctx.Log.Debug("failed to parse block",
			zap.Stringer("nodeID", nodeID),
			zap.Uint32("requestID", requestID),
			zap.Error(err),
		)

		t.metrics.numUselessPutBytes.Add(float64(len(blkBytes)))
		t.blocked.Abandon(ctx, expectedBlkID)
		return t.executeDeferredWork(ctx)
	}

	if actualBlkID := blk.ID(); actualBlkID != expectedBlkID {
		t.Ctx.Log.Debug("incorrect block returned in Put",
			zap.Stringer("nodeID", nodeID),
			zap.Uint32("requestID", requestID),
			zap.Stringer("blkID", actualBlkID),
			zap.Stringer("expectedBlkID", expectedBlkID),
		)

		t.metrics.numUselessPutBytes.Add(float64(len(blkBytes)))
		t.blocked.Abandon(ctx, expectedBlkID)
		return t.executeDeferredWork(ctx)
	}

	if !shouldBlockBeQueuedForIssuance(t.Consensus, t.pending, blk) {
		t.metrics.numUselessPutBytes.Add(float64(len(blkBytes)))
	}

	// issue the block into consensus. If the block has already been issued,
	// this will be a noop. If this block has missing dependencies, nodeID will
	// receive requests to fill the ancestry. dependencies that have already
	// been fetched, but with missing dependencies themselves won't be requested
	// from the vdr.
	if _, err := t.issueChain(ctx, nodeID, blk, issuedMetric); err != nil {
		return err
	}
	return t.executeDeferredWork(ctx)
}

func (t *Transitive) GetFailed(ctx context.Context, nodeID ids.NodeID, requestID uint32) error {
	request := common.Request{
		NodeID:    nodeID,
		RequestID: requestID,
	}
	blkID, ok := t.blkReqs.DeleteKey(request)
	if !ok {
		t.Ctx.Log.Debug("unexpected GetFailed",
			zap.Stringer("nodeID", nodeID),
			zap.Uint32("requestID", requestID),
		)
		return nil
	}
	delete(t.blkReqSourceMetric, request)

	// Because the get request was dropped, we no longer expect blkID to be
	// issued.
	t.blocked.Abandon(ctx, blkID)
	return t.executeDeferredWork(ctx)
}

func (t *Transitive) PullQuery(ctx context.Context, nodeID ids.NodeID, requestID uint32, blkID ids.ID, requestedHeight uint64) error {
	t.sendChits(ctx, nodeID, requestID, requestedHeight)

	issuedMetric := t.metrics.issued.WithLabelValues(pushGossipSource)
	if _, err := t.issueID(ctx, nodeID, blkID, issuedMetric); err != nil {
		return err
	}

	return t.executeDeferredWork(ctx)
}

func (t *Transitive) PushQuery(ctx context.Context, nodeID ids.NodeID, requestID uint32, blkBytes []byte, requestedHeight uint64) error {
	t.sendChits(ctx, nodeID, requestID, requestedHeight)

	blk, err := t.VM.ParseBlock(ctx, blkBytes)
	// If parsing fails, we just drop the request, as we didn't ask for it
	if err != nil {
		t.Ctx.Log.Debug("failed to parse block",
			zap.Stringer("nodeID", nodeID),
			zap.Uint32("requestID", requestID),
			zap.Error(err),
		)
		return nil
	}

	if !shouldBlockBeQueuedForIssuance(t.Consensus, t.pending, blk) {
		t.metrics.numUselessPushQueryBytes.Add(float64(len(blkBytes)))
	}

	// issue the block into consensus. If the block has already been issued,
	// this will be a noop. If this block has missing dependencies, nodeID will
	// receive requests to fill the ancestry. dependencies that have already
	// been fetched, but with missing dependencies themselves won't be requested
	// from the vdr.
	issuedMetric := t.metrics.issued.WithLabelValues(pushGossipSource)
	if _, err := t.issueChain(ctx, nodeID, blk, issuedMetric); err != nil {
		return err
	}
	return t.executeDeferredWork(ctx)
}

func (t *Transitive) Chits(ctx context.Context, nodeID ids.NodeID, requestID uint32, preferredID ids.ID, preferredIDAtHeight ids.ID, acceptedID ids.ID) error {
	t.acceptedFrontiers.SetLastAccepted(nodeID, acceptedID)

	t.Ctx.Log.Verbo("called Chits for the block",
		zap.Stringer("nodeID", nodeID),
		zap.Uint32("requestID", requestID),
		zap.Stringer("preferredID", preferredID),
		zap.Stringer("preferredIDAtHeight", preferredIDAtHeight),
		zap.Stringer("acceptedID", acceptedID),
	)

	issuedMetric := t.metrics.issued.WithLabelValues(pullGossipSource)
	shouldWaitOnPreferred, err := t.issueID(ctx, nodeID, preferredID, issuedMetric)
	if err != nil {
		return err
	}

	var (
		shouldWaitOnPreferredIDAtHeight bool
		// Invariant: The order of [responseOptions] must be [preferredID] then
		// (optionally) [preferredIDAtHeight]. During vote application, the
		// first vote that can be applied will be used. So, the votes should be
		// populated in order of decreasing height.
		responseOptions = []ids.ID{preferredID}
	)
	if preferredID != preferredIDAtHeight {
		shouldWaitOnPreferredIDAtHeight, err = t.issueID(ctx, nodeID, preferredIDAtHeight, issuedMetric)
		if err != nil {
			return err
		}
		responseOptions = append(responseOptions, preferredIDAtHeight)
	}

	// Will record chits once [preferredID] and [preferredIDAtHeight] have been
	// issued into consensus
	v := &voter{
		t:               t,
		vdr:             nodeID,
		requestID:       requestID,
		responseOptions: responseOptions,
	}

	// Wait until [preferredID] and [preferredIDAtHeight] have been issued to
	// consensus before applying this chit.
	if shouldWaitOnPreferred {
		v.deps.Add(preferredID)
	}
	if shouldWaitOnPreferredIDAtHeight {
		v.deps.Add(preferredIDAtHeight)
	}

	t.blocked.Register(ctx, v)
	return t.executeDeferredWork(ctx)
}

func (t *Transitive) QueryFailed(ctx context.Context, nodeID ids.NodeID, requestID uint32) error {
	lastAccepted, ok := t.acceptedFrontiers.LastAccepted(nodeID)
	if ok {
		return t.Chits(ctx, nodeID, requestID, lastAccepted, lastAccepted, lastAccepted)
	}

	t.blocked.Register(
		ctx,
		&voter{
			t:         t,
			vdr:       nodeID,
			requestID: requestID,
		},
	)
	return t.executeDeferredWork(ctx)
}

func (*Transitive) Timeout(context.Context) error {
	return nil
}

func (*Transitive) Halt(context.Context) {}

func (t *Transitive) Shutdown(ctx context.Context) error {
	t.Ctx.Log.Info("shutting down consensus engine")

	t.Ctx.Lock.Lock()
	defer t.Ctx.Lock.Unlock()

	return t.VM.Shutdown(ctx)
}

func (t *Transitive) Notify(ctx context.Context, msg common.Message) error {
	switch msg {
	case common.PendingTxs:
		// the pending txs message means we should attempt to build a block.
		t.pendingBuildBlocks++
		return t.executeDeferredWork(ctx)
	case common.StateSyncDone:
		t.Ctx.StateSyncing.Set(false)
		return nil
	default:
		t.Ctx.Log.Warn("received an unexpected message from the VM",
			zap.Stringer("messageString", msg),
		)
		return nil
	}
}

func (t *Transitive) Context() *snow.ConsensusContext {
	return t.Ctx
}

func (t *Transitive) Start(ctx context.Context, startReqID uint32) error {
	t.requestID = startReqID
	lastAcceptedID, err := t.VM.LastAccepted(ctx)
	if err != nil {
		return err
	}

	lastAccepted, err := t.getBlock(ctx, lastAcceptedID)
	if err != nil {
		t.Ctx.Log.Error("failed to get last accepted block",
			zap.Error(err),
		)
		return err
	}

	// initialize consensus to the last accepted blockID
	lastAcceptedHeight := lastAccepted.Height()
	if err := t.Consensus.Initialize(t.Ctx, t.Params, lastAcceptedID, lastAcceptedHeight, lastAccepted.Timestamp()); err != nil {
		return err
	}

	// to maintain the invariant that oracle blocks are issued in the correct
	// preferences, we need to handle the case that we are bootstrapping into an oracle block
	if oracleBlk, ok := lastAccepted.(snowman.OracleBlock); ok {
		options, err := oracleBlk.Options(ctx)
		switch {
		case err == snowman.ErrNotOracle:
			// if there aren't blocks we need to deliver on startup, we need to set
			// the preference to the last accepted block
			if err := t.VM.SetPreference(ctx, lastAcceptedID); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			issuedMetric := t.metrics.issued.WithLabelValues(builtSource)
			for _, blk := range options {
				// note that deliver will set the VM's preference
				if err := t.deliver(ctx, t.Ctx.NodeID, blk, false, issuedMetric); err != nil {
					return err
				}
			}
		}
	} else if err := t.VM.SetPreference(ctx, lastAcceptedID); err != nil {
		return err
	}

	t.Ctx.Log.Info("starting consensus",
		zap.Stringer("lastAcceptedID", lastAcceptedID),
		zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
	)
	t.metrics.bootstrapFinished.Set(1)

	t.Ctx.State.Set(snow.EngineState{
		Type:  p2p.EngineType_ENGINE_TYPE_SNOWMAN,
		State: snow.NormalOp,
	})
	if err := t.VM.SetState(ctx, snow.NormalOp); err != nil {
		return fmt.Errorf("failed to notify VM that consensus is starting: %w",
			err)
	}
	return t.executeDeferredWork(ctx)
}

func (t *Transitive) HealthCheck(ctx context.Context) (interface{}, error) {
	t.Ctx.Lock.Lock()
	defer t.Ctx.Lock.Unlock()

	t.Ctx.Log.Verbo("running health check",
		zap.Uint32("requestID", t.requestID),
		zap.Stringer("polls", t.polls),
		zap.Reflect("outstandingBlockRequests", t.blkReqs),
		zap.Stringer("blockedJobs", &t.blocked),
		zap.Int("pendingBuildBlocks", t.pendingBuildBlocks),
	)

	consensusIntf, consensusErr := t.Consensus.HealthCheck(ctx)
	vmIntf, vmErr := t.VM.HealthCheck(ctx)
	intf := map[string]interface{}{
		"consensus": consensusIntf,
		"vm":        vmIntf,
	}
	if consensusErr == nil {
		return intf, vmErr
	}
	if vmErr == nil {
		return intf, consensusErr
	}
	return intf, fmt.Errorf("vm: %w ; consensus: %w", vmErr, consensusErr)
}

func (t *Transitive) executeDeferredWork(ctx context.Context) error {
	if t.errs.Errored() {
		return t.errs.Err
	}

	// Build blocks if they have been requested and the number of processing
	// blocks is less than optimal.
	for t.pendingBuildBlocks > 0 && t.Consensus.NumProcessing() < t.Params.OptimalProcessing {
		t.pendingBuildBlocks--

		if err := t.buildBlock(ctx); err != nil {
			return err
		}
	}

	if t.Consensus.NumProcessing() > 0 {
		// if we are issuing a repoll, we should gossip our current preferences
		// to propagate the most likely branch as quickly as possible.
		preferredID := t.Consensus.Preference()

		for i := t.polls.Len(); i < t.Params.ConcurrentRepolls; i++ {
			t.sendQuery(ctx, preferredID, nil, false)
		}
	}

	t.metrics.numRequests.Set(float64(t.blkReqs.Len()))
	t.metrics.numBlocked.Set(float64(len(t.pending)))
	t.metrics.numBlockers.Set(float64(t.blocked.Len()))
	t.metrics.numNonVerifieds.Set(float64(t.nonVerifieds.Len()))
	return t.errs.Err
}

func (t *Transitive) getBlock(ctx context.Context, blkID ids.ID) (snowman.Block, error) {
	if blk, ok := t.pending[blkID]; ok {
		return blk, nil
	}
	if blk, ok := t.nonVerifiedCache.Get(blkID); ok {
		return blk, nil
	}

	return t.VM.GetBlock(ctx, blkID)
}

func (t *Transitive) sendChits(ctx context.Context, nodeID ids.NodeID, requestID uint32, requestedHeight uint64) {
	lastAcceptedID, lastAcceptedHeight := t.Consensus.LastAccepted()
	// If we aren't fully verifying blocks, only vote for blocks that are widely
	// preferred by the validator set.
	if t.Ctx.StateSyncing.Get() || t.Config.PartialSync {
		acceptedAtHeight, err := t.VM.GetBlockIDAtHeight(ctx, requestedHeight)
		if err != nil {
			// Because we only return accepted state here, it's fairly likely
			// that the requested height is higher than the last accepted block.
			// That means that this code path is actually quite common.
			t.Ctx.Log.Debug("failed fetching accepted block",
				zap.Stringer("nodeID", nodeID),
				zap.Uint64("requestedHeight", requestedHeight),
				zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
				zap.Stringer("lastAcceptedID", lastAcceptedID),
				zap.Error(err),
			)
			acceptedAtHeight = lastAcceptedID
		}
		t.Sender.SendChits(ctx, nodeID, requestID, lastAcceptedID, acceptedAtHeight, lastAcceptedID)
		return
	}

	var (
		preference         = t.Consensus.Preference()
		preferenceAtHeight ids.ID
	)
	if requestedHeight < lastAcceptedHeight {
		var err error
		preferenceAtHeight, err = t.VM.GetBlockIDAtHeight(ctx, requestedHeight)
		if err != nil {
			// If this chain is pruning historical blocks, it's expected for a
			// node to be unable to fetch some block IDs. In this case, we fall
			// back to returning the last accepted ID.
			//
			// Because it is possible for a byzantine node to spam requests at
			// old heights on a pruning network, we log this as debug. However,
			// this case is unexpected to be hit by correct peers.
			t.Ctx.Log.Debug("failed fetching accepted block",
				zap.Stringer("nodeID", nodeID),
				zap.Uint64("requestedHeight", requestedHeight),
				zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
				zap.Stringer("lastAcceptedID", lastAcceptedID),
				zap.Error(err),
			)
			t.numMissingAcceptedBlocks.Inc()

			preferenceAtHeight = lastAcceptedID
		}
	} else {
		var ok bool
		preferenceAtHeight, ok = t.Consensus.PreferenceAtHeight(requestedHeight)
		if !ok {
			t.Ctx.Log.Debug("failed fetching processing block",
				zap.Stringer("nodeID", nodeID),
				zap.Uint64("requestedHeight", requestedHeight),
				zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
				zap.Stringer("preferredID", preference),
			)
			// If the requested height is higher than our preferred tip, we
			// don't prefer anything at the requested height yet.
			preferenceAtHeight = preference
		}
	}
	t.Sender.SendChits(ctx, nodeID, requestID, preference, preferenceAtHeight, lastAcceptedID)
}

func (t *Transitive) buildBlock(ctx context.Context) error {
	blk, err := t.VM.BuildBlock(ctx)
	if err != nil {
		t.Ctx.Log.Debug("failed building block",
			zap.Error(err),
		)
		t.numBuildsFailed.Inc()
		return nil
	}
	t.numBuilt.Inc()

	blkID := blk.ID()
	parentID := blk.Parent()
	if shouldBlockBeDropped(t.Consensus, blk) {
		lastAcceptedID, lastAcceptedHeight := t.Consensus.LastAccepted()
		t.Ctx.Log.Warn("dropping newly built block",
			zap.Stringer("blkID", blkID),
			zap.Stringer("parentID", parentID),
			zap.Uint64("blkHeight", blk.Height()),
			zap.Stringer("lastAcceptedID", lastAcceptedID),
			zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
		)
		return nil
	}

	if !canBlockHaveChildIssued(t.Consensus, parentID) {
		lastAcceptedID, lastAcceptedHeight := t.Consensus.LastAccepted()
		t.Ctx.Log.Warn("newly built block can't be issued",
			zap.Stringer("blkID", blkID),
			zap.Stringer("parentID", parentID),
			zap.Uint64("blkHeight", blk.Height()),
			zap.Stringer("lastAcceptedID", lastAcceptedID),
			zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
		)
		return nil
	}

	// The newly created block should be built on top of the preferred
	// block. Otherwise, the new block doesn't have the best chance of being
	// confirmed.
	if pref := t.Consensus.Preference(); parentID != pref {
		t.Ctx.Log.Warn("built block with unexpected parent",
			zap.Stringer("expectedParentID", pref),
			zap.Stringer("parentID", parentID),
		)
	}

	// Remove any outstanding requests for this block
	//
	// Because [canBlockHaveChildIssued] returned true, we know that there
	// isn't an outstanding issue job for this block already. However, there
	// may have already been a request for this block.
	if req, ok := t.blkReqs.DeleteValue(blkID); ok {
		delete(t.blkReqSourceMetric, req)
	}

	issuedMetric := t.metrics.issued.WithLabelValues(builtSource)
	return t.deliver(ctx, t.Ctx.NodeID, blk, true, issuedMetric)
}

// issueID attempts to issue the branch ending with [blkID] into consensus.
// Returns true if it is safe to register a dependency onto [blkID].
// If an ancestor is missing, it will be requested from [nodeID].
func (t *Transitive) issueID(
	ctx context.Context,
	nodeID ids.NodeID,
	blkID ids.ID,
	issuedMetric prometheus.Counter,
) (bool, error) {
	blk, err := t.getBlock(ctx, blkID)
	if err != nil {
		t.sendRequest(ctx, nodeID, blkID, issuedMetric)
		return true, nil
	}
	return t.issueChain(ctx, nodeID, blk, issuedMetric)
}

// issueChain attempts to issue the chain of blocks ending with [blk] to
// consensus.
// Returns true if it is safe to register a dependency onto [blk].
// If an ancestor is missing, it will be requested from [nodeID].
func (t *Transitive) issueChain(
	ctx context.Context,
	nodeID ids.NodeID,
	blk snowman.Block,
	issuedMetric prometheus.Counter,
) (bool, error) {
	for {
		blkID := blk.ID()
		// Remove any outstanding requests for this block
		if req, ok := t.blkReqs.DeleteValue(blkID); ok {
			delete(t.blkReqSourceMetric, req)
		}

		// If this block is accepted, all the jobs should have already been
		// fulfilled. If this block is rejected, it's possible that there are
		// still jobs pending its issuance. So we must abandon those jobs.
		if isBlockDecided(t.Consensus, blk) {
			t.blocked.Abandon(ctx, blkID)
			return false, t.errs.Err
		}

		// I'm either the last accepted block or processing. In either case, no
		// one should register a dependency on me.
		if canBlockHaveChildIssued(t.Consensus, blkID) {
			return false, nil
		}

		// This block is already pending issuance. So, jobs can be queued on top
		// of me.
		if _, isPending := t.pending[blkID]; isPending {
			return true, nil
		}

		// If our parent can have a child issued, we can be issued.
		parentID := blk.Parent()
		if canBlockHaveChildIssued(t.Consensus, parentID) {
			// Delivering the block here will either abandon or fulfill the
			// block.
			return false, t.deliver(ctx, nodeID, blk, false, issuedMetric)
		}

		// mark that the block is queued to be added to consensus once its ancestors
		// have been
		t.pending[blkID] = blk
		// Will add [blk] to consensus once its ancestors have been
		t.blocked.Register(ctx, &issuer{
			t:            t,
			nodeID:       nodeID,
			blk:          blk,
			issuedMetric: issuedMetric,
			deps:         set.Of(parentID),
			push:         false,
		})

		var err error
		blk, err = t.getBlock(ctx, parentID)
		// If we don't have the parent block, we should request it.
		if err != nil {
			t.sendRequest(ctx, nodeID, blkID, issuedMetric)
			return true, nil
		}
	}
}

// Request that [nodeID] send us block [blkID]
func (t *Transitive) sendRequest(
	ctx context.Context,
	nodeID ids.NodeID,
	blkID ids.ID,
	issuedMetric prometheus.Counter,
) {
	// There is already an outstanding request for this block
	if t.blkReqs.HasValue(blkID) {
		return
	}

	t.requestID++
	req := common.Request{
		NodeID:    nodeID,
		RequestID: t.requestID,
	}
	t.blkReqs.Put(req, blkID)
	t.blkReqSourceMetric[req] = issuedMetric

	t.Ctx.Log.Verbo("sending Get request",
		zap.Stringer("nodeID", nodeID),
		zap.Uint32("requestID", t.requestID),
		zap.Stringer("blkID", blkID),
	)
	t.Sender.SendGet(ctx, nodeID, t.requestID, blkID)
}

// Send a query for this block. If push is set to true, blkBytes will be used to
// send a PushQuery. Otherwise, blkBytes will be ignored and a PullQuery will be
// sent.
func (t *Transitive) sendQuery(
	ctx context.Context,
	blkID ids.ID,
	blkBytes []byte,
	push bool,
) {
	t.Ctx.Log.Verbo("sampling from validators",
		zap.Stringer("validators", t.Validators),
	)

	vdrIDs, err := t.Validators.Sample(t.Ctx.SubnetID, t.Params.K)
	if err != nil {
		t.Ctx.Log.Warn("dropped query for block",
			zap.String("reason", "insufficient number of validators"),
			zap.Stringer("blkID", blkID),
			zap.Int("size", t.Params.K),
		)
		return
	}

	_, lastAcceptedHeight := t.Consensus.LastAccepted()
	nextHeightToAccept, err := math.Add64(lastAcceptedHeight, 1)
	if err != nil {
		t.Ctx.Log.Error("dropped query for block",
			zap.String("reason", "block height overflow"),
			zap.Stringer("blkID", blkID),
			zap.Uint64("lastAcceptedHeight", lastAcceptedHeight),
			zap.Error(err),
		)
		return
	}

	vdrBag := bag.Of(vdrIDs...)
	t.requestID++
	if !t.polls.Add(t.requestID, vdrBag) {
		t.Ctx.Log.Error("dropped query for block",
			zap.String("reason", "failed to add poll"),
			zap.Stringer("blkID", blkID),
			zap.Uint32("requestID", t.requestID),
		)
		return
	}

	vdrSet := set.Of(vdrIDs...)
	if push {
		t.Sender.SendPushQuery(ctx, vdrSet, t.requestID, blkBytes, nextHeightToAccept)
	} else {
		t.Sender.SendPullQuery(ctx, vdrSet, t.requestID, blkID, nextHeightToAccept)
	}
}

// issue [blk] to consensus
// If [push] is true, a push query will be used. Otherwise, a pull query will be
// used.
func (t *Transitive) deliver(
	ctx context.Context,
	nodeID ids.NodeID,
	blk snowman.Block,
	push bool,
	issuedMetric prometheus.Counter,
) error {
	var (
		blksToIssue   = make([]snowman.Block, 1, 3)
		blksToFulfill = make([]snowman.Block, 0, 3)
		blksToAbandon = make([]ids.ID, 0, 3)
	)
	blksToIssue[0] = blk
	for len(blksToIssue) > 0 {
		blk := blksToIssue[0]
		blksToIssue = blksToIssue[1:]

		blkID := blk.ID()

		// If the block has already been issued, we don't need to issue it again
		if shouldBlockBeDropped(t.Consensus, blk) {
			blksToAbandon = append(blksToAbandon, blkID)
			continue
		}

		parentID := blk.Parent()
		if !canBlockHaveChildIssued(t.Consensus, parentID) {
			blksToAbandon = append(blksToAbandon, blkID)
			continue
		}

		// make sure this block is valid
		blkHeight := blk.Height()
		if err := blk.Verify(ctx); err != nil {
			t.Ctx.Log.Debug("block verification failed",
				zap.Stringer("nodeID", nodeID),
				zap.Stringer("blkID", blkID),
				zap.Uint64("height", blkHeight),
				zap.Error(err),
			)

			// if verify fails, then all descendants are also invalid
			t.addToNonVerifieds(blk)
			blksToAbandon = append(blksToAbandon, blkID)
			continue
		}

		issuedMetric.Inc()
		t.nonVerifieds.Remove(blkID)
		t.nonVerifiedCache.Evict(blkID)
		t.metrics.issuerStake.Observe(float64(t.Validators.GetWeight(t.Ctx.SubnetID, nodeID)))
		t.Ctx.Log.Verbo("adding block to consensus",
			zap.Stringer("nodeID", nodeID),
			zap.Stringer("blkID", blkID),
			zap.Uint64("height", blkHeight),
		)
		err := t.Consensus.Add(&memoryBlock{
			Block:   blk,
			metrics: t.metrics,
			tree:    t.nonVerifieds,
		})
		if err != nil {
			return err
		}

		blksToFulfill = append(blksToFulfill, blk)

		oracleBlock, ok := blk.(snowman.OracleBlock)
		if !ok {
			continue
		}

		options, err := oracleBlock.Options(ctx)
		if err == snowman.ErrNotOracle {
			continue
		}
		if err != nil {
			return err
		}

		blksToIssue = append(blksToIssue, options[:]...)
	}

	if err := t.VM.SetPreference(ctx, t.Consensus.Preference()); err != nil {
		return err
	}

	for _, blk := range blksToFulfill {
		blkID := blk.ID()
		if t.Consensus.IsPreferred(blkID) {
			t.sendQuery(ctx, blkID, blk.Bytes(), push)
		}

		delete(t.pending, blkID)
		if req, ok := t.blkReqs.DeleteValue(blkID); ok {
			delete(t.blkReqSourceMetric, req)
		}
		t.blocked.Fulfill(ctx, blkID)
	}
	for _, blkID := range blksToAbandon {
		delete(t.pending, blkID)
		if req, ok := t.blkReqs.DeleteValue(blkID); ok {
			delete(t.blkReqSourceMetric, req)
		}
		t.blocked.Abandon(ctx, blkID)
	}
	return t.errs.Err
}

func (t *Transitive) addToNonVerifieds(blk snowman.Block) {
	// don't add this blk if it's decided or processing.
	if shouldBlockBeDropped(t.Consensus, blk) {
		return
	}

	parentID := blk.Parent()
	// we might still need this block so we can bubble votes to the parent
	// only add blocks with parent already in the tree or processing.
	// decided parents should not be in this map.
	if t.nonVerifieds.Has(parentID) || t.Consensus.Processing(parentID) {
		blkID := blk.ID()
		t.nonVerifieds.Add(blkID, parentID)
		t.nonVerifiedCache.Put(blkID, blk)
	}
}

// addUnverifiedBlockToConsensus returns whether the block was added and an
// error if one occurred while adding it to consensus.
func (t *Transitive) addUnverifiedBlockToConsensus(
	ctx context.Context,
	nodeID ids.NodeID,
	blk snowman.Block,
	issuedMetric prometheus.Counter,
) (bool, error) {
	blkID := blk.ID()
	blkHeight := blk.Height()

	// make sure this block is valid
	if err := blk.Verify(ctx); err != nil {
		t.Ctx.Log.Debug("block verification failed",
			zap.Stringer("nodeID", nodeID),
			zap.Stringer("blkID", blkID),
			zap.Uint64("height", blkHeight),
			zap.Error(err),
		)

		// if verify fails, then all descendants are also invalid
		t.addToNonVerifieds(blk)
		return false, nil
	}

	issuedMetric.Inc()
	t.nonVerifieds.Remove(blkID)
	t.nonVerifiedCache.Evict(blkID)
	t.metrics.issuerStake.Observe(float64(t.Validators.GetWeight(t.Ctx.SubnetID, nodeID)))
	t.Ctx.Log.Verbo("adding block to consensus",
		zap.Stringer("nodeID", nodeID),
		zap.Stringer("blkID", blkID),
		zap.Uint64("height", blkHeight),
	)
	return true, t.Consensus.Add(&memoryBlock{
		Block:   blk,
		metrics: t.metrics,
		tree:    t.nonVerifieds,
	})
}

// getProcessingAncestor finds [initialVote]'s most recent ancestor that is
// processing in consensus. If no ancestor could be found, false is returned.
//
// Note: If [initialVote] is processing, then [initialVote] will be returned.
func (t *Transitive) getProcessingAncestor(ctx context.Context, initialVote ids.ID) (ids.ID, bool) {
	// If [bubbledVote] != [initialVote], it is guaranteed that [bubbledVote] is
	// in processing. Otherwise, we attempt to iterate through any blocks we
	// have at our disposal as a best-effort mechanism to find a valid ancestor.
	bubbledVote := t.nonVerifieds.GetAncestor(initialVote)
	_, lastAcceptedHeight := t.Consensus.LastAccepted()
	lastUsefulHeight := lastAcceptedHeight + 1
	for {
		if t.Consensus.Processing(bubbledVote) {
			t.Ctx.Log.Verbo("applying vote",
				zap.Stringer("initialVoteID", initialVote),
				zap.Stringer("bubbledVoteID", bubbledVote),
			)
			if bubbledVote != initialVote {
				t.numGetProcessingAncestorResults.WithLabelValues(ancestorResult).Inc()
			} else {
				t.numGetProcessingAncestorResults.WithLabelValues(selfResult).Inc()
			}
			return bubbledVote, true
		}

		blk, err := t.getBlock(ctx, bubbledVote)
		// If we cannot retrieve the block, drop [vote]
		if err != nil {
			t.Ctx.Log.Debug("dropping vote",
				zap.String("reason", "ancestor couldn't be fetched"),
				zap.Stringer("initialVoteID", initialVote),
				zap.Stringer("bubbledVoteID", bubbledVote),
				zap.Error(err),
			)
			t.numGetProcessingAncestorResults.WithLabelValues(missingResult).Inc()
			return ids.Empty, false
		}

		// If the parent block height wouldn't be useful, we just drop the vote.
		if height := blk.Height(); height <= lastUsefulHeight {
			t.Ctx.Log.Debug("dropping vote",
				zap.String("reason", "bubbled vote already decided"),
				zap.Stringer("initialVoteID", initialVote),
				zap.Stringer("bubbledVoteID", bubbledVote),
				zap.Uint64("height", height),
			)
			t.numGetProcessingAncestorResults.WithLabelValues(decidedResult).Inc()
			return ids.Empty, false
		}

		bubbledVote = blk.Parent()
	}
}

func shouldBlockBeQueuedForIssuance(
	consensus snowman.Consensus,
	pendingBlocks map[ids.ID]snowman.Block,
	blk snowman.Block,
) bool {
	if shouldBlockBeDropped(consensus, blk) {
		return false
	}

	blkID := blk.ID()
	_, alreadyQueued := pendingBlocks[blkID]
	return !alreadyQueued
}

// shouldBlockBeDropped returns true if [blk] should not be issued into
// consensus.
//
// A block should not be issued into consensus if its already processing, or the
// block's height indicates that it has already been decided.
func shouldBlockBeDropped(consensus snowman.Consensus, blk snowman.Block) bool {
	blkID := blk.ID()
	return consensus.Processing(blkID) || isBlockDecided(consensus, blk)
}

// isBlockDecided returns true if [blk]'s height indicates that it has been
// decided.
func isBlockDecided(consensus snowman.Consensus, blk snowman.Block) bool {
	blkHeight := blk.Height()
	lastAcceptedID, lastAcceptedHeight := consensus.LastAccepted()
	if blkHeight <= lastAcceptedHeight {
		return true
	}

	var (
		parentID           = blk.Parent()
		nextHeightToAccept = lastAcceptedHeight + 1
	)
	return blkHeight == nextHeightToAccept && parentID != lastAcceptedID
}

// canBlockHaveChildIssued returns true if a block with [parentID] can be issued
// into consensus.
//
// A block can be issued into consensus if its parent is either processing or is
// the most recently accepted block.
func canBlockHaveChildIssued(consensus snowman.Consensus, parentID ids.ID) bool {
	lastAcceptedID, _ := consensus.LastAccepted()
	return parentID == lastAcceptedID || consensus.Processing(parentID)
}
