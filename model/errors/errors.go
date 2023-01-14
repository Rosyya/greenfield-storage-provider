package errors

import (
	"errors"

	service "github.com/bnb-chain/greenfield-storage-provider/service/types/v1"
)

// piece store errors
var (
	NotSupportedMethod          = errors.New("not supported method")
	NotSupportedDelimiter       = errors.New("not supported delimiter")
	EmptyObjectKey              = errors.New("object key cannot be empty")
	EmptyMemoryObject           = errors.New("memory object is empty")
	BucketNotExisted            = errors.New("bucket not existed")
	ErrNoPermissionAccessBucket = errors.New("no permission to access the bucket")
)

// gateway errors
var (
	ErrInternalError    = errors.New("internal error")
	ErrDuplicateBucket  = errors.New("duplicate bucket")
	ErrDuplicateObject  = errors.New("duplicate object")
	ErrObjectTxNotExist = errors.New("object tx not exist")
	ErrObjectNotExist   = errors.New("object not exist")
	ErrObjectIsEmpty    = errors.New("object payload is empty")
)

// stone hub service errors
var (
	ErrObjectInfoNil               = errors.New("object info is nil")
	ErrObjectIdZero                = errors.New("object id is zero")
	ErrObjectSizeZero              = errors.New("object size is zero")
	ErrObjectHeightZero            = errors.New("object create height is zero")
	ErrPrimarySPMismatch           = errors.New("primary storage provider id mismatch")
	ErrStorageProviderMissing      = errors.New("storage provider missing")
	ErrUploadPayloadJobRunning     = errors.New("upload payload job is running")
	ErrUploadPayloadJobNotExist    = errors.New("upload payload job not exist")
	ErrPieceJobMissing             = errors.New("piece job missing")
	ErrSealInfoMissing             = errors.New("seal info missing")
	ErrSpJobNotCompleted           = errors.New("job not completed")
	ErrCheckSumCountMismatch       = errors.New("checksum count mismatch")
	ErrCheckSumLengthMismatch      = errors.New("check sum length not equal 32 bytes")
	ErrIntegrityHashLengthMismatch = errors.New("integrity hash length not equal 32 bytes")
	ErrSignatureLengthMismatch     = errors.New("signature length not equal 32 bytes")
	ErrIndexOutOfBounds            = errors.New("array index out of bounds")
	ErrStoneJobTypeUnrecognized    = errors.New("unrecognized stone job type")
	ErrInterfaceAbandoned          = errors.New("interface is abandoned")
)

// stone node service errors
var (
	ErrStoneNodeStarted   = errors.New("stone node resource is running")
	ErrStoneNodeStopped   = errors.New("stone node service has stopped")
	ErrIntegrityHash      = errors.New("secondary integrity hash check error")
	ErrRedundancyType     = errors.New("unknown redundancy type")
	ErrEmptyJob           = errors.New("job is empty")
	ErrSecondarySPNumber  = errors.New("secondary sp is not enough")
	ErrInvalidSegmentData = errors.New("invalid segment data, length is not equal to 1")
	ErrInvalidECData      = errors.New("invalid ec data, length is not equal to 6")
	ErrEmptyTargetIdx     = errors.New("target index array is empty")
)

// syncer service errors
var (
	ErrReceivedPieceCount = errors.New("syncer service received piece count is wrong")
)

func MakeErrMsgResponse(err error) *service.ErrMessage {
	return &service.ErrMessage{
		ErrCode: service.ErrCode_ERR_CODE_ERROR,
		ErrMsg:  err.Error(),
	}
}