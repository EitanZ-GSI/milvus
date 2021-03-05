package dataservice

import (
	"context"

	"github.com/zilliztech/milvus-distributed/internal/proto/commonpb"
	"github.com/zilliztech/milvus-distributed/internal/proto/masterpb"
)

type allocator interface {
	allocTimestamp() (Timestamp, error)
	allocID() (UniqueID, error)
}

type allocatorImpl struct {
	masterClient MasterClient
}

func newAllocatorImpl(masterClient MasterClient) *allocatorImpl {
	return &allocatorImpl{
		masterClient: masterClient,
	}
}

func (allocator *allocatorImpl) allocTimestamp() (Timestamp, error) {
	ctx := context.TODO()
	resp, err := allocator.masterClient.AllocTimestamp(ctx, &masterpb.TsoRequest{
		Base: &commonpb.MsgBase{
			MsgType:   commonpb.MsgType_kShowCollections,
			MsgID:     -1, // todo add msg id
			Timestamp: 0,  // todo
			SourceID:  Params.NodeID,
		},
		Count: 1,
	})
	if err = VerifyResponse(resp, err); err != nil {
		return 0, err
	}
	return resp.Timestamp, nil
}

func (allocator *allocatorImpl) allocID() (UniqueID, error) {
	ctx := context.TODO()
	resp, err := allocator.masterClient.AllocID(ctx, &masterpb.IDRequest{
		Base: &commonpb.MsgBase{
			MsgType:   commonpb.MsgType_kShowCollections,
			MsgID:     -1, // todo add msg id
			Timestamp: 0,  // todo
			SourceID:  Params.NodeID,
		},
		Count: 1,
	})
	if err = VerifyResponse(resp, err); err != nil {
		return 0, err
	}

	return resp.ID, nil
}