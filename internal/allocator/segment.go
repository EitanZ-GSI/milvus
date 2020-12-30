package allocator

import (
	"container/list"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/zilliztech/milvus-distributed/internal/proto/commonpb"

	"github.com/zilliztech/milvus-distributed/internal/errors"
	"github.com/zilliztech/milvus-distributed/internal/proto/internalpb"
)

const (
	SegCountPerRPC     = 20000
	ActiveTimeDuration = 100 //second
)

type segInfo struct {
	segID      UniqueID
	count      uint32
	expireTime Timestamp
}

type assignInfo struct {
	collName       string
	partitionTag   string
	channelID      int32
	segID          UniqueID
	segInfos       *list.List
	segCapacity    uint32
	lastInsertTime time.Time
}

func (info *segInfo) IsExpired(ts Timestamp) bool {
	return ts > info.expireTime || info.count <= 0
}

func (info *segInfo) Capacity(ts Timestamp) uint32 {
	if info.IsExpired(ts) {
		return 0
	}
	return info.count
}

func (info *segInfo) Assign(ts Timestamp, count uint32) uint32 {
	if info.IsExpired(ts) {
		return 0
	}
	ret := uint32(0)
	if info.count >= count {
		info.count -= count
		ret = count
	} else {
		info.count = 0
		ret = info.count
	}
	return ret
}

func (info *assignInfo) RemoveExpired(ts Timestamp) {
	for e := info.segInfos.Front(); e != nil; e = e.Next() {
		segInfo := e.Value.(*segInfo)
		if segInfo.IsExpired(ts) {
			info.segInfos.Remove(e)
		}
	}
}

func (info *assignInfo) Capacity(ts Timestamp) uint32 {
	ret := uint32(0)
	for e := info.segInfos.Front(); e != nil; e = e.Next() {
		segInfo := e.Value.(*segInfo)
		ret += segInfo.Capacity(ts)
	}
	return ret
}

func (info *assignInfo) Assign(ts Timestamp, count uint32) (map[UniqueID]uint32, error) {
	capacity := info.Capacity(ts)
	if capacity < count {
		errMsg := fmt.Sprintf("AssignSegment Failed: capacity:%d is less than count:%d", capacity, count)
		return nil, errors.New(errMsg)
	}

	result := make(map[UniqueID]uint32)
	for e := info.segInfos.Front(); e != nil && count != 0; e = e.Next() {
		segInfo := e.Value.(*segInfo)
		cur := segInfo.Assign(ts, count)
		count -= cur
		if cur > 0 {
			result[segInfo.segID] += cur
		}
	}
	return result, nil
}

func (info *assignInfo) IsActive(now time.Time) bool {
	return now.Sub(info.lastInsertTime) <= ActiveTimeDuration*time.Second
}

type SegIDAssigner struct {
	Allocator
	assignInfos map[string]*list.List // collectionName -> *list.List
	segReqs     []*internalpb.SegIDRequest
	getTickFunc func() Timestamp
	PeerID      UniqueID
}

func NewSegIDAssigner(ctx context.Context, masterAddr string, getTickFunc func() Timestamp) (*SegIDAssigner, error) {
	ctx1, cancel := context.WithCancel(ctx)
	sa := &SegIDAssigner{
		Allocator: Allocator{reqs: make(chan request, maxConcurrentRequests),
			ctx:           ctx1,
			cancel:        cancel,
			masterAddress: masterAddr,
			countPerRPC:   SegCountPerRPC,
		},
		assignInfos: make(map[string]*list.List),
		getTickFunc: getTickFunc,
	}
	sa.tChan = &ticker{
		updateInterval: time.Second,
	}
	sa.Allocator.syncFunc = sa.syncSegments
	sa.Allocator.processFunc = sa.processFunc
	sa.Allocator.checkSyncFunc = sa.checkSyncFunc
	sa.Allocator.pickCanDoFunc = sa.pickCanDoFunc
	return sa, nil
}

func (sa *SegIDAssigner) collectExpired() {
	ts := sa.getTickFunc()
	//now := time.Now()
	for _, info := range sa.assignInfos {
		for e := info.Front(); e != nil; e = e.Next() {
			assign := e.Value.(*assignInfo)
			assign.RemoveExpired(ts)
			if assign.Capacity(ts) == 0 {
				info.Remove(e)
				//if assign.IsActive(now) {
				//	sa.segReqs = append(sa.segReqs, &internalpb.SegIDRequest{
				//		ChannelID:    assign.channelID,
				//		Count:        0, // intend to set zero
				//		CollName:     assign.collName,
				//		PartitionTag: assign.partitionTag,
				//	})
				//} else {
				//	info.Remove(e)
				//}
			}
		}
	}
}

func (sa *SegIDAssigner) pickCanDoFunc() {
	if sa.toDoReqs == nil {
		return
	}
	records := make(map[string]map[string]map[int32]uint32)
	newTodoReqs := sa.toDoReqs[0:0]
	for _, req := range sa.toDoReqs {
		segRequest := req.(*segRequest)
		colName := segRequest.colName
		partition := segRequest.partition
		channelID := segRequest.channelID

		if _, ok := records[colName]; !ok {
			records[colName] = make(map[string]map[int32]uint32)
		}
		if _, ok := records[colName][partition]; !ok {
			records[colName][partition] = make(map[int32]uint32)
		}

		if _, ok := records[colName][partition][channelID]; !ok {
			records[colName][partition][channelID] = 0
		}

		records[colName][partition][channelID] += segRequest.count
		assign := sa.getAssign(segRequest.colName, segRequest.partition, segRequest.channelID)
		if assign == nil || assign.Capacity(segRequest.timestamp) < records[colName][partition][channelID] {
			sa.segReqs = append(sa.segReqs, &internalpb.SegIDRequest{
				ChannelID:    segRequest.channelID,
				Count:        segRequest.count,
				CollName:     segRequest.colName,
				PartitionTag: segRequest.partition,
			})
			newTodoReqs = append(newTodoReqs, req)
		} else {
			sa.canDoReqs = append(sa.canDoReqs, req)
		}
	}
	sa.toDoReqs = newTodoReqs
}

func (sa *SegIDAssigner) getAssign(colName, partition string, channelID int32) *assignInfo {
	assignInfos, ok := sa.assignInfos[colName]
	if !ok {
		return nil
	}

	for e := assignInfos.Front(); e != nil; e = e.Next() {
		info := e.Value.(*assignInfo)
		if info.partitionTag != partition || info.channelID != channelID {
			continue
		}
		return info
	}
	return nil
}

func (sa *SegIDAssigner) checkSyncFunc(timeout bool) bool {
	sa.collectExpired()
	return timeout || len(sa.segReqs) != 0
}

func (sa *SegIDAssigner) checkSegReqEqual(req1, req2 *internalpb.SegIDRequest) bool {
	if req1 == nil || req2 == nil {
		return false
	}

	if req1 == req2 {
		return true
	}
	return req1.CollName == req2.CollName && req1.PartitionTag == req2.PartitionTag && req1.ChannelID == req2.ChannelID
}

func (sa *SegIDAssigner) reduceSegReqs() {

	if len(sa.segReqs) == 0 {
		return
	}

	var newSegReqs []*internalpb.SegIDRequest
	for _, req1 := range sa.segReqs {
		var req2 *internalpb.SegIDRequest
		for _, req3 := range newSegReqs {
			if sa.checkSegReqEqual(req1, req3) {
				req2 = req3
				break
			}
		}
		if req2 == nil { // not found
			newSegReqs = append(newSegReqs, req1)
		} else {
			req2.Count += req1.Count
		}
	}

	for _, req := range newSegReqs {
		if req.Count == 0 {
			req.Count = sa.countPerRPC
		}
	}
	sa.segReqs = newSegReqs
}

func (sa *SegIDAssigner) syncSegments() bool {
	if len(sa.segReqs) == 0 {
		return true
	}
	sa.reduceSegReqs()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := &internalpb.AssignSegIDRequest{
		PeerID:        sa.PeerID,
		Role:          internalpb.PeerRole_Proxy,
		PerChannelReq: sa.segReqs,
	}

	sa.segReqs = []*internalpb.SegIDRequest{}
	resp, err := sa.masterClient.AssignSegmentID(ctx, req)

	if err != nil {
		log.Println("GRPC AssignSegmentID Failed", resp, err)
		return false
	}

	now := time.Now()
	success := false
	for _, info := range resp.PerChannelAssignment {
		if info.Status.GetErrorCode() != commonpb.ErrorCode_SUCCESS {
			log.Println("SyncSegment Error:", info.Status.Reason)
			continue
		}
		assign := sa.getAssign(info.CollName, info.PartitionTag, info.ChannelID)
		segInfo := &segInfo{
			segID:      info.SegID,
			count:      info.Count,
			expireTime: info.ExpireTime,
		}
		if assign == nil {
			colInfos, ok := sa.assignInfos[info.CollName]
			if !ok {
				colInfos = list.New()
			}
			segInfos := list.New()

			segInfos.PushBack(segInfo)
			assign = &assignInfo{
				collName:     info.CollName,
				partitionTag: info.PartitionTag,
				channelID:    info.ChannelID,
				segInfos:     segInfos,
			}
			colInfos.PushBack(assign)
			sa.assignInfos[info.CollName] = colInfos
		} else {
			assign.segInfos.PushBack(segInfo)
		}
		assign.lastInsertTime = now
		success = true
	}
	return success
}

func (sa *SegIDAssigner) processFunc(req request) error {
	segRequest := req.(*segRequest)
	assign := sa.getAssign(segRequest.colName, segRequest.partition, segRequest.channelID)
	if assign == nil {
		return errors.New("Failed to GetSegmentID")
	}
	result, err := assign.Assign(segRequest.timestamp, segRequest.count)
	segRequest.segInfo = result
	return err
}

func (sa *SegIDAssigner) GetSegmentID(colName, partition string, channelID int32, count uint32, ts Timestamp) (map[UniqueID]uint32, error) {
	req := &segRequest{
		baseRequest: baseRequest{done: make(chan error), valid: false},
		colName:     colName,
		partition:   partition,
		channelID:   channelID,
		count:       count,
		timestamp:   ts,
	}
	sa.reqs <- req
	req.Wait()

	if !req.IsValid() {
		return nil, errors.New("GetSegmentID Failed")
	}
	return req.segInfo, nil
}