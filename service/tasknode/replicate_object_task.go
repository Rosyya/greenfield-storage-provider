package tasknode

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"sync"

	sdkmath "cosmossdk.io/math"
	"github.com/bnb-chain/greenfield-common/go/redundancy"
	"github.com/bnb-chain/greenfield-storage-provider/model"
	merrors "github.com/bnb-chain/greenfield-storage-provider/model/errors"
	"github.com/bnb-chain/greenfield-storage-provider/model/piecestore"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/log"
	p2ptypes "github.com/bnb-chain/greenfield-storage-provider/pkg/p2p/types"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/rcmgr"
	gatewayclient "github.com/bnb-chain/greenfield-storage-provider/service/gateway/client"
	servicetypes "github.com/bnb-chain/greenfield-storage-provider/service/types"
	"github.com/bnb-chain/greenfield-storage-provider/util/maps"
	sptypes "github.com/bnb-chain/greenfield/x/sp/types"
	"github.com/bnb-chain/greenfield/x/storage/types"
	storagetypes "github.com/bnb-chain/greenfield/x/storage/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// ReplicateFactor defines the redundancy of replication
	// TODO:: will update to （1, 2] on main net
	ReplicateFactor = 1
	// GetApprovalTimeout defines the timeout of getting secondary sp approval
	GetApprovalTimeout = 10
)

// streamReader is used to stream produce/consume piece data stream.
type streamReader struct {
	pRead  *io.PipeReader
	pWrite *io.PipeWriter
}

// Read populates the given byte slice with data and returns the number of bytes populated and an error value.
// It returns an io.EOF error when the stream ends.
func (s *streamReader) Read(buf []byte) (n int, err error) {
	return s.pRead.Read(buf)
}

// streamReaderGroup is used to the primary sp replicate object piece data to other secondary sps.
type streamReaderGroup struct {
	task            *replicateObjectTask
	pieceSize       int
	streamReaderMap map[int]*streamReader
}

// newStreamReaderGroup returns a streamReaderGroup
func newStreamReaderGroup(t *replicateObjectTask, excludeIndexMap map[int]bool) (*streamReaderGroup, error) {
	var atLeastHasOne bool
	sg := &streamReaderGroup{
		task:            t,
		streamReaderMap: make(map[int]*streamReader),
	}
	for segmentPieceIdx := 0; segmentPieceIdx < t.segmentPieceNumber; segmentPieceIdx++ {
		for idx := 0; idx < t.redundancyNumber; idx++ {
			if excludeIndexMap[idx] {
				continue
			}
			sg.streamReaderMap[idx] = &streamReader{}
			sg.streamReaderMap[idx].pRead, sg.streamReaderMap[idx].pWrite = io.Pipe()
			atLeastHasOne = true
		}
	}
	if !atLeastHasOne {
		return nil, merrors.ErrInvalidParams
	}
	return sg, nil
}

// produceStreamPieceData produce stream piece data
func (sg *streamReaderGroup) produceStreamPieceData() {
	ch := make(chan int)
	go func(pieceSizeCh chan int) {
		defer close(pieceSizeCh)
		gotPieceSize := false

		for segmentPieceIdx := 0; segmentPieceIdx < sg.task.segmentPieceNumber; segmentPieceIdx++ {
			segmentPiecekey := piecestore.EncodeSegmentPieceKey(sg.task.objectInfo.Id.Uint64(), uint32(segmentPieceIdx))
			segmentPieceData, err := sg.task.taskNode.pieceStore.GetPiece(context.Background(), segmentPiecekey, 0, 0)
			if err != nil {
				for idx := range sg.streamReaderMap {
					sg.streamReaderMap[idx].pWrite.CloseWithError(err)
				}
				log.Errorw("failed to get piece data", "piece_key", segmentPiecekey, "error", err)
				return
			}
			if sg.task.objectInfo.GetRedundancyType() == types.REDUNDANCY_EC_TYPE {
				ecPieceData, err := redundancy.EncodeRawSegment(segmentPieceData,
					int(sg.task.storageParams.GetRedundantDataChunkNum()),
					int(sg.task.storageParams.GetRedundantParityChunkNum()))
				if err != nil {
					for idx := range sg.streamReaderMap {
						sg.streamReaderMap[idx].pWrite.CloseWithError(err)
					}
					log.Errorw("failed to encode ec piece data", "error", err)
					return
				}
				if !gotPieceSize {
					pieceSizeCh <- len(ecPieceData[0])
					gotPieceSize = true
				}
				for idx := range sg.streamReaderMap {
					sg.streamReaderMap[idx].pWrite.Write(ecPieceData[idx])
					log.Debugw("succeed to produce an ec piece data", "piece_len", len(ecPieceData[idx]), "redundancy_index", idx)
				}
			} else {
				if !gotPieceSize {
					pieceSizeCh <- len(segmentPieceData)
					gotPieceSize = true
				}
				for idx := range sg.streamReaderMap {
					sg.streamReaderMap[idx].pWrite.Write(segmentPieceData)
					log.Debugw("succeed to produce an segment piece data", "piece_len", len(segmentPieceData), "redundancy_index", idx)
				}
			}
		}
		for idx := range sg.streamReaderMap {
			sg.streamReaderMap[idx].pWrite.Close()
			log.Debugw("succeed to finish a piece stream",
				"redundancy_index", idx, "redundancy_type", sg.task.objectInfo.GetRedundancyType())
		}
	}(ch)
	sg.pieceSize = <-ch
}

// streamPieceDataReplicator replicates a piece stream to the target sp
type streamPieceDataReplicator struct {
	task                  *replicateObjectTask
	pieceSize             uint32
	redundancyIndex       uint32
	expectedIntegrityHash []byte
	streamReader          *streamReader
	sp                    *sptypes.StorageProvider
	approval              *p2ptypes.GetApprovalResponse
}

// replicate is used to start replicate the piece stream
func (r *streamPieceDataReplicator) replicate() (integrityHash []byte, signature []byte, err error) {
	var (
		gwClient      *gatewayclient.GatewayClient
		originMsgHash []byte
		approvalAddr  sdk.AccAddress
	)

	gwClient, err = gatewayclient.NewGatewayClient(r.sp.GetEndpoint())
	if err != nil {
		log.Errorw("failed to create gateway client",
			"sp_endpoint", r.sp.GetEndpoint(), "error", err)
		return
	}
	integrityHash, signature, err = gwClient.ReplicateObjectPieceStream(r.task.objectInfo.Id.Uint64(), r.pieceSize,
		r.redundancyIndex, r.approval, r.streamReader)
	if err != nil {
		log.Errorw("failed to replicate object piece stream",
			"endpoint", r.sp.GetEndpoint(), "error", err)
		return
	}
	if !bytes.Equal(r.expectedIntegrityHash, integrityHash) {
		err = merrors.ErrMismatchIntegrityHash
		log.Errorw("failed to check root hash",
			"expected", hex.EncodeToString(r.expectedIntegrityHash),
			"actual", hex.EncodeToString(integrityHash), "error", err)
		return
	}

	// verify secondary signature
	originMsgHash = storagetypes.NewSecondarySpSignDoc(r.sp.GetOperator(), sdkmath.NewUint(r.task.objectInfo.Id.Uint64()), integrityHash).GetSignBytes()
	approvalAddr, err = sdk.AccAddressFromHexUnsafe(r.sp.GetApprovalAddress())
	if err != nil {
		log.Errorw("failed to parse sp operator address",
			"sp", r.sp.GetApprovalAddress(), "endpoint", r.sp.GetEndpoint(),
			"error", err)
		return
	}
	err = storagetypes.VerifySignature(approvalAddr, sdk.Keccak256(originMsgHash), signature)
	if err != nil {
		log.Errorw("failed to verify sp signature",
			"sp", r.sp.GetApprovalAddress(), "endpoint", r.sp.GetEndpoint(), "error", err)
		return
	}

	return integrityHash, signature, nil
}

// replicateObjectTask represents the background object replicate task, include replica/ec redundancy type.
type replicateObjectTask struct {
	ctx                 context.Context
	taskNode            *TaskNode
	objectInfo          *types.ObjectInfo
	approximateMemSize  int
	storageParams       *storagetypes.Params
	segmentPieceNumber  int
	redundancyNumber    int
	mux                 sync.Mutex
	spMap               map[string]*sptypes.StorageProvider
	approvalResponseMap map[string]*p2ptypes.GetApprovalResponse
	sortedSpEndpoints   []string
}

// newReplicateObjectTask returns a ReplicateObjectTask instance
func newReplicateObjectTask(ctx context.Context, task *TaskNode, object *types.ObjectInfo) (*replicateObjectTask, error) {
	if ctx == nil || task == nil || object == nil {
		return nil, merrors.ErrInvalidParams
	}
	return &replicateObjectTask{
		ctx:                 ctx,
		taskNode:            task,
		objectInfo:          object,
		spMap:               make(map[string]*sptypes.StorageProvider),
		approvalResponseMap: make(map[string]*p2ptypes.GetApprovalResponse),
	}, nil
}

// updateTaskState is used to update task state.
func (t *replicateObjectTask) updateTaskState(state servicetypes.JobState) error {
	return t.taskNode.spDB.UpdateJobState(t.objectInfo.Id.Uint64(), state)
}

// init is used to synchronize the resources which is needed to initialize the task.
func (t *replicateObjectTask) init() error {
	var err error
	t.storageParams, err = t.taskNode.spDB.GetStorageParams()
	if err != nil {
		log.CtxErrorw(t.ctx, "failed to query sp params", "error", err)
		return err
	}
	t.segmentPieceNumber = int(piecestore.ComputeSegmentCount(t.objectInfo.GetPayloadSize(),
		t.storageParams.GetMaxSegmentSize()))
	t.redundancyNumber = int(t.storageParams.GetRedundantDataChunkNum() + t.storageParams.GetRedundantParityChunkNum())
	if t.redundancyNumber+1 != len(t.objectInfo.GetChecksums()) {
		log.CtxError(t.ctx, "failed to init due to redundancy number is not equal to checksums")
		return merrors.ErrInvalidParams
	}
	t.spMap, t.approvalResponseMap, err = t.taskNode.getApproval(
		t.objectInfo, t.redundancyNumber, t.redundancyNumber*ReplicateFactor, GetApprovalTimeout)
	if err != nil {
		log.CtxErrorw(t.ctx, "failed to get approvals", "error", err)
		return err
	}
	t.sortedSpEndpoints = maps.SortKeys(t.approvalResponseMap)
	// reserve memory
	t.approximateMemSize = int(float64(t.storageParams.GetMaxSegmentSize()) *
		(float64(t.redundancyNumber)/float64(t.storageParams.GetRedundantDataChunkNum()) + 1))
	if t.objectInfo.GetPayloadSize() < t.storageParams.GetMaxSegmentSize() {
		t.approximateMemSize = int(float64(t.objectInfo.GetPayloadSize()) *
			(float64(t.redundancyNumber)/float64(t.storageParams.GetRedundantDataChunkNum()) + 1))
	}
	err = t.taskNode.rcScope.ReserveMemory(t.approximateMemSize, rcmgr.ReservationPriorityAlways)
	if err != nil {
		log.CtxErrorw(t.ctx, "failed to reserve memory from resource manager",
			"reserve_size", t.approximateMemSize, "error", err)
		return err
	}
	log.CtxDebugw(t.ctx, "reserve memory from resource manager",
		"reserve_size", t.approximateMemSize, "resource_state", rcmgr.GetServiceState(model.TaskNodeService))
	return nil
}

// execute is used to start the task.
func (t *replicateObjectTask) execute() {
	var (
		sealMsg         *storagetypes.MsgSealObject
		progressInfo    *servicetypes.ReplicatePieceInfo
		succeedIndexMap map[int]bool
	)
	defer func() {
		t.taskNode.rcScope.ReleaseMemory(t.approximateMemSize)
		log.CtxDebugw(t.ctx, "release memory to resource manager",
			"release_size", t.approximateMemSize, "resource_state", rcmgr.GetServiceState(model.TaskNodeService))
	}()

	t.updateTaskState(servicetypes.JobState_JOB_STATE_REPLICATE_OBJECT_DOING)

	succeedIndexMap = make(map[int]bool, t.redundancyNumber)
	isAllSucceed := func(inputIndexMap map[int]bool) bool {
		for i := 0; i < t.redundancyNumber; i++ {
			if !inputIndexMap[i] {
				return false
			}
		}
		return true
	}
	pickSp := func() (sp *sptypes.StorageProvider, approval *p2ptypes.GetApprovalResponse, err error) {
		t.mux.Lock()
		defer t.mux.Unlock()
		if len(t.approvalResponseMap) == 0 {
			log.CtxError(t.ctx, "backup storage providers exhausted")
			err = merrors.ErrExhaustedSP
			return
		}
		endpoint := t.sortedSpEndpoints[0]
		sp = t.spMap[endpoint]
		approval = t.approvalResponseMap[endpoint]
		t.sortedSpEndpoints = t.sortedSpEndpoints[1:]
		delete(t.spMap, endpoint)
		delete(t.approvalResponseMap, endpoint)
		return
	}
	sealMsg = &storagetypes.MsgSealObject{
		Operator:              t.taskNode.config.SpOperatorAddress,
		BucketName:            t.objectInfo.GetBucketName(),
		ObjectName:            t.objectInfo.GetObjectName(),
		SecondarySpAddresses:  make([]string, t.redundancyNumber),
		SecondarySpSignatures: make([][]byte, t.redundancyNumber),
	}
	t.objectInfo.SecondarySpAddresses = make([]string, t.redundancyNumber)
	progressInfo = &servicetypes.ReplicatePieceInfo{
		PieceInfos: make([]*servicetypes.PieceInfo, t.redundancyNumber),
	}

	for {
		if isAllSucceed(succeedIndexMap) {
			log.CtxInfo(t.ctx, "succeed to replicate object data")
			break
		}
		sg, err := newStreamReaderGroup(t, succeedIndexMap)
		if err != nil {
			log.CtxErrorw(t.ctx, "failed to new stream reader group", "error", err)
			return
		}
		if len(sg.streamReaderMap) > len(t.sortedSpEndpoints) {
			log.CtxError(t.ctx, "failed to replicate due to sp is not enough")
			return
		}
		sg.produceStreamPieceData()

		var wg sync.WaitGroup
		for redundancyIdx := range sg.streamReaderMap {
			wg.Add(1)
			go func(rIdx int) {
				defer wg.Done()

				sp, approval, innerErr := pickSp()
				if innerErr != nil {
					log.CtxErrorw(t.ctx, "failed to pick a secondary sp", "redundancy_index", rIdx, "error", innerErr)
					return
				}
				r := &streamPieceDataReplicator{
					task:                  t,
					pieceSize:             uint32(sg.pieceSize),
					redundancyIndex:       uint32(rIdx),
					expectedIntegrityHash: sg.task.objectInfo.GetChecksums()[rIdx+1],
					streamReader:          sg.streamReaderMap[rIdx],
					sp:                    sp,
					approval:              approval,
				}
				integrityHash, signature, innerErr := r.replicate()
				if innerErr != nil {
					log.CtxErrorw(t.ctx, "failed to replicate piece stream", "redundancy_index", rIdx, "error", innerErr)
					return
				}

				succeedIndexMap[rIdx] = true
				sealMsg.GetSecondarySpAddresses()[rIdx] = sp.GetOperator().String()
				sealMsg.GetSecondarySpSignatures()[rIdx] = signature
				progressInfo.PieceInfos[rIdx] = &servicetypes.PieceInfo{
					ObjectInfo:    t.objectInfo,
					Signature:     signature,
					IntegrityHash: integrityHash,
				}
				t.objectInfo.SecondarySpAddresses[rIdx] = sp.GetOperator().String()
				t.taskNode.spDB.SetObjectInfo(t.objectInfo.Id.Uint64(), t.objectInfo)
				t.taskNode.cache.Add(t.objectInfo.Id.Uint64(), progressInfo)
				log.CtxInfow(t.ctx, "succeed to replicate object piece stream to the target sp",
					"sp", sp.GetOperator(), "endpoint", sp.GetEndpoint(), "redundancy_index", rIdx)

			}(redundancyIdx)
		}
		wg.Wait()
	}

	// seal info
	if isAllSucceed(succeedIndexMap) {
		t.updateTaskState(servicetypes.JobState_JOB_STATE_SIGN_OBJECT_DOING)
		_, err := t.taskNode.signer.SealObjectOnChain(context.Background(), sealMsg)
		if err != nil {
			t.taskNode.spDB.UpdateJobState(t.objectInfo.Id.Uint64(), servicetypes.JobState_JOB_STATE_SIGN_OBJECT_ERROR)
			log.CtxErrorw(t.ctx, "failed to sign object by signer", "error", err)
			return
		}
		t.updateTaskState(servicetypes.JobState_JOB_STATE_SEAL_OBJECT_DOING)
		err = t.taskNode.chain.ListenObjectSeal(context.Background(), t.objectInfo.GetBucketName(),
			t.objectInfo.GetObjectName(), 10)
		if err != nil {
			t.updateTaskState(servicetypes.JobState_JOB_STATE_SEAL_OBJECT_ERROR)
			log.CtxErrorw(t.ctx, "failed to seal object on chain", "error", err)
			return
		}
		t.updateTaskState(servicetypes.JobState_JOB_STATE_SEAL_OBJECT_DONE)
		log.CtxInfo(t.ctx, "succeed to seal object on chain")
	} else {
		err := t.updateTaskState(servicetypes.JobState_JOB_STATE_REPLICATE_OBJECT_ERROR)
		log.CtxErrorw(t.ctx, "failed to replicate object data to sp", "error", err, "succeed_index_map", succeedIndexMap)
		return
	}
}