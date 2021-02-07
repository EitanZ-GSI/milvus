package masterservice

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/assert"
	cms "github.com/zilliztech/milvus-distributed/internal/masterservice"
	"github.com/zilliztech/milvus-distributed/internal/proto/commonpb"
	"github.com/zilliztech/milvus-distributed/internal/proto/datapb"
	"github.com/zilliztech/milvus-distributed/internal/proto/internalpb2"
	"github.com/zilliztech/milvus-distributed/internal/proto/milvuspb"
	"github.com/zilliztech/milvus-distributed/internal/proto/schemapb"
	"github.com/zilliztech/milvus-distributed/internal/util/typeutil"
)

func TestGrpcService(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	randVal := rand.Int()

	//cms.Params.Address = "127.0.0.1"
	cms.Params.Port = (randVal % 100) + 10000

	svr, err := NewGrpcServer(context.Background())
	assert.Nil(t, err)

	// cms.Params.NodeID = 0
	//cms.Params.PulsarAddress = "pulsar://127.0.0.1:6650"
	//cms.Params.EtcdAddress = "127.0.0.1:2379"
	cms.Params.MetaRootPath = fmt.Sprintf("/%d/test/meta", randVal)
	cms.Params.KvRootPath = fmt.Sprintf("/%d/test/kv", randVal)
	cms.Params.ProxyTimeTickChannel = fmt.Sprintf("proxyTimeTick%d", randVal)
	cms.Params.MsgChannelSubName = fmt.Sprintf("msgChannel%d", randVal)
	cms.Params.TimeTickChannel = fmt.Sprintf("timeTick%d", randVal)
	cms.Params.DdChannel = fmt.Sprintf("ddChannel%d", randVal)
	cms.Params.StatisticsChannel = fmt.Sprintf("stateChannel%d", randVal)
	cms.Params.DataServiceSegmentChannel = fmt.Sprintf("segmentChannel%d", randVal)

	cms.Params.MaxPartitionNum = 64
	cms.Params.DefaultPartitionName = "_default"
	cms.Params.DefaultIndexName = "_default"

	t.Logf("master service port = %d", cms.Params.Port)

	core := svr.core.(*cms.Core)

	err = svr.Init()
	assert.Nil(t, err)

	core.ProxyTimeTickChan = make(chan typeutil.Timestamp, 8)
	core.DataNodeSegmentFlushCompletedChan = make(chan typeutil.UniqueID, 8)

	timeTickArray := make([]typeutil.Timestamp, 0, 16)
	core.SendTimeTick = func(ts typeutil.Timestamp) error {
		t.Logf("send time tick %d", ts)
		timeTickArray = append(timeTickArray, ts)
		return nil
	}
	createCollectionArray := make([]*internalpb2.CreateCollectionRequest, 0, 16)
	core.DdCreateCollectionReq = func(req *internalpb2.CreateCollectionRequest) error {
		t.Logf("Create Colllection %s", req.CollectionName)
		createCollectionArray = append(createCollectionArray, req)
		return nil
	}

	dropCollectionArray := make([]*internalpb2.DropCollectionRequest, 0, 16)
	core.DdDropCollectionReq = func(req *internalpb2.DropCollectionRequest) error {
		t.Logf("Drop Collection %s", req.CollectionName)
		dropCollectionArray = append(dropCollectionArray, req)
		return nil
	}

	createPartitionArray := make([]*internalpb2.CreatePartitionRequest, 0, 16)
	core.DdCreatePartitionReq = func(req *internalpb2.CreatePartitionRequest) error {
		t.Logf("Create Partition %s", req.PartitionName)
		createPartitionArray = append(createPartitionArray, req)
		return nil
	}

	dropPartitionArray := make([]*internalpb2.DropPartitionRequest, 0, 16)
	core.DdDropPartitionReq = func(req *internalpb2.DropPartitionRequest) error {
		t.Logf("Drop Partition %s", req.PartitionName)
		dropPartitionArray = append(dropPartitionArray, req)
		return nil
	}

	core.DataServiceSegmentChan = make(chan *datapb.SegmentInfo, 1024)

	core.GetBinlogFilePathsFromDataServiceReq = func(segID typeutil.UniqueID, fieldID typeutil.UniqueID) ([]string, error) {
		return []string{"file1", "file2", "file3"}, nil
	}

	var binlogLock sync.Mutex
	binlogPathArray := make([]string, 0, 16)
	core.BuildIndexReq = func(binlog []string, typeParams []*commonpb.KeyValuePair, indexParams []*commonpb.KeyValuePair, indexID typeutil.UniqueID, indexName string) (typeutil.UniqueID, error) {
		binlogLock.Lock()
		defer binlogLock.Unlock()
		binlogPathArray = append(binlogPathArray, binlog...)
		return 2000, nil
	}

	collectionMetaCache := make([]string, 0, 16)
	core.InvalidateCollectionMetaCache = func(ts typeutil.Timestamp, dbName string, collectionName string) error {
		collectionMetaCache = append(collectionMetaCache, collectionName)
		return nil
	}

	core.ReleaseCollection = func(ts typeutil.Timestamp, dbID typeutil.UniqueID, collectionID typeutil.UniqueID) error {
		return nil
	}

	err = svr.Start()
	assert.Nil(t, err)

	cli, err := NewGrpcClient(fmt.Sprintf("%s:%d", cms.Params.Address, cms.Params.Port), 3*time.Second)
	assert.Nil(t, err)

	err = cli.Init()
	assert.Nil(t, err)

	err = cli.Start()
	assert.Nil(t, err)

	t.Run("create collection", func(t *testing.T) {
		schema := schemapb.CollectionSchema{
			Name:        "testColl",
			Description: "testColl",
			AutoID:      true,
			Fields: []*schemapb.FieldSchema{
				{
					FieldID:      100,
					Name:         "vector",
					IsPrimaryKey: false,
					Description:  "vector",
					DataType:     schemapb.DataType_VECTOR_FLOAT,
					TypeParams:   nil,
					IndexParams: []*commonpb.KeyValuePair{
						{
							Key:   "ik1",
							Value: "iv1",
						},
					},
				},
			},
		}

		sbf, err := proto.Marshal(&schema)
		assert.Nil(t, err)

		req := &milvuspb.CreateCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kCreateCollection,
				MsgID:     100,
				Timestamp: 100,
				SourceID:  100,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
			Schema:         sbf,
		}

		status, err := cli.CreateCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, len(createCollectionArray), 1)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, createCollectionArray[0].Base.MsgType, commonpb.MsgType_kCreateCollection)
		assert.Equal(t, createCollectionArray[0].CollectionName, "testColl")

		req.Base.MsgID = 101
		req.Base.Timestamp = 101
		req.Base.SourceID = 101
		status, err = cli.CreateCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_UNEXPECTED_ERROR)

		req.Base.MsgID = 102
		req.Base.Timestamp = 102
		req.Base.SourceID = 102
		req.CollectionName = "testColl-again"
		status, err = cli.CreateCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_UNEXPECTED_ERROR)

		schema.Name = req.CollectionName
		sbf, err = proto.Marshal(&schema)
		assert.Nil(t, err)
		req.Schema = sbf
		req.Base.MsgID = 103
		req.Base.Timestamp = 103
		req.Base.SourceID = 103
		status, err = cli.CreateCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, len(createCollectionArray), 2)
		assert.Equal(t, createCollectionArray[1].Base.MsgType, commonpb.MsgType_kCreateCollection)
		assert.Equal(t, createCollectionArray[1].CollectionName, "testColl-again")

		//time stamp go back
		schema.Name = "testColl-goback"
		sbf, err = proto.Marshal(&schema)
		assert.Nil(t, err)
		req.CollectionName = schema.Name
		req.Schema = sbf
		req.Base.MsgID = 103
		req.Base.Timestamp = 103
		req.Base.SourceID = 103
		status, err = cli.CreateCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_UNEXPECTED_ERROR)
		matched, err := regexp.MatchString("input timestamp = [0-9]+, last dd time stamp = [0-9]+", status.Reason)
		assert.Nil(t, err)
		assert.True(t, matched)
	})

	t.Run("has collection", func(t *testing.T) {
		req := &milvuspb.HasCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kHasCollection,
				MsgID:     110,
				Timestamp: 110,
				SourceID:  110,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
		}
		rsp, err := cli.HasCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, rsp.Value, true)

		req = &milvuspb.HasCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kHasCollection,
				MsgID:     111,
				Timestamp: 111,
				SourceID:  111,
			},
			DbName:         "testDb",
			CollectionName: "testColl2",
		}
		rsp, err = cli.HasCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, rsp.Value, false)

		// test time stamp go back
		req = &milvuspb.HasCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kHasCollection,
				MsgID:     111,
				Timestamp: 111,
				SourceID:  111,
			},
			DbName:         "testDb",
			CollectionName: "testColl2",
		}
		rsp, err = cli.HasCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, rsp.Value, false)
	})

	t.Run("describe collection", func(t *testing.T) {
		collMeta, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		req := &milvuspb.DescribeCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kDescribeCollection,
				MsgID:     120,
				Timestamp: 120,
				SourceID:  120,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
		}
		rsp, err := cli.DescribeCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, rsp.Schema.Name, "testColl")
		assert.Equal(t, rsp.CollectionID, collMeta.ID)
	})

	t.Run("show collection", func(t *testing.T) {
		req := &milvuspb.ShowCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kShowCollections,
				MsgID:     130,
				Timestamp: 130,
				SourceID:  130,
			},
			DbName: "testDb",
		}
		rsp, err := cli.ShowCollections(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.ElementsMatch(t, rsp.CollectionNames, []string{"testColl", "testColl-again"})
		assert.Equal(t, len(rsp.CollectionNames), 2)
	})

	t.Run("create partition", func(t *testing.T) {
		req := &milvuspb.CreatePartitionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kCreatePartition,
				MsgID:     140,
				Timestamp: 140,
				SourceID:  140,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
			PartitionName:  "testPartition",
		}
		status, err := cli.CreatePartition(req)
		assert.Nil(t, err)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		collMeta, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		assert.Equal(t, len(collMeta.PartitionIDs), 2)
		partMeta, err := core.MetaTable.GetPartitionByID(collMeta.PartitionIDs[1])
		assert.Nil(t, err)
		assert.Equal(t, partMeta.PartitionName, "testPartition")

	})

	t.Run("has partition", func(t *testing.T) {
		req := &milvuspb.HasPartitionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kHasPartition,
				MsgID:     150,
				Timestamp: 150,
				SourceID:  150,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
			PartitionName:  "testPartition",
		}
		rsp, err := cli.HasPartition(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, rsp.Value, true)
	})

	t.Run("show partition", func(t *testing.T) {
		coll, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		req := &milvuspb.ShowPartitionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kShowPartitions,
				MsgID:     160,
				Timestamp: 160,
				SourceID:  160,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
			CollectionID:   coll.ID,
		}
		rsp, err := cli.ShowPartitions(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, len(rsp.PartitionNames), 2)
		assert.Equal(t, len(rsp.PartitionIDs), 2)
	})

	t.Run("show segment", func(t *testing.T) {
		coll, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		partID := coll.PartitionIDs[1]
		part, err := core.MetaTable.GetPartitionByID(partID)
		assert.Nil(t, err)
		assert.Zero(t, len(part.SegmentIDs))
		seg := &datapb.SegmentInfo{
			SegmentID:    1000,
			CollectionID: coll.ID,
			PartitionID:  part.PartitionID,
		}
		core.DataServiceSegmentChan <- seg
		time.Sleep(time.Millisecond * 100)
		part, err = core.MetaTable.GetPartitionByID(partID)
		assert.Nil(t, err)
		assert.Equal(t, len(part.SegmentIDs), 1)

		req := &milvuspb.ShowSegmentRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kShowSegment,
				MsgID:     170,
				Timestamp: 170,
				SourceID:  170,
			},
			CollectionID: coll.ID,
			PartitionID:  partID,
		}
		rsp, err := cli.ShowSegments(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, rsp.SegmentIDs[0], int64(1000))
		assert.Equal(t, len(rsp.SegmentIDs), 1)
	})

	t.Run("create index", func(t *testing.T) {
		req := &milvuspb.CreateIndexRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kCreateIndex,
				MsgID:     180,
				Timestamp: 180,
				SourceID:  180,
			},
			DbName:         "",
			CollectionName: "testColl",
			FieldName:      "vector",
			ExtraParams: []*commonpb.KeyValuePair{
				{
					Key:   "ik1",
					Value: "iv1",
				},
			},
		}
		collMeta, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		assert.Equal(t, len(collMeta.IndexParams), 1)
		rsp, err := cli.CreateIndex(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.ErrorCode, commonpb.ErrorCode_SUCCESS)
		collMeta, err = core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		assert.Equal(t, len(collMeta.IndexParams), 1)

		binlogLock.Lock()
		defer binlogLock.Unlock()
		assert.Equal(t, 3, len(binlogPathArray))
		assert.ElementsMatch(t, binlogPathArray, []string{"file1", "file2", "file3"})

		req.FieldName = "no field"
		rsp, err = cli.CreateIndex(req)
		assert.Nil(t, err)
		assert.NotEqual(t, rsp.ErrorCode, commonpb.ErrorCode_SUCCESS)
	})

	t.Run("describe segment", func(t *testing.T) {
		coll, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)

		req := &milvuspb.DescribeSegmentRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kDescribeSegment,
				MsgID:     190,
				Timestamp: 190,
				SourceID:  190,
			},
			CollectionID: coll.ID,
			SegmentID:    1000,
		}
		rsp, err := cli.DescribeSegment(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		t.Logf("index id = %d", rsp.IndexID)
	})

	t.Run("describe index", func(t *testing.T) {
		req := &milvuspb.DescribeIndexRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kDescribeIndex,
				MsgID:     200,
				Timestamp: 200,
				SourceID:  200,
			},
			DbName:         "",
			CollectionName: "testColl",
			FieldName:      "vector",
			IndexName:      "",
		}
		rsp, err := cli.DescribeIndex(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, len(rsp.IndexDescriptions), 1)
		assert.Equal(t, rsp.IndexDescriptions[0].IndexName, cms.Params.DefaultIndexName)
	})

	t.Run("flush segment", func(t *testing.T) {
		coll, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		partID := coll.PartitionIDs[1]
		part, err := core.MetaTable.GetPartitionByID(partID)
		assert.Nil(t, err)
		assert.Equal(t, len(part.SegmentIDs), 1)
		seg := &datapb.SegmentInfo{
			SegmentID:    1001,
			CollectionID: coll.ID,
			PartitionID:  part.PartitionID,
		}
		core.DataServiceSegmentChan <- seg
		time.Sleep(time.Millisecond * 100)
		part, err = core.MetaTable.GetPartitionByID(partID)
		assert.Nil(t, err)
		assert.Equal(t, len(part.SegmentIDs), 2)
		core.DataNodeSegmentFlushCompletedChan <- 1001
		time.Sleep(time.Millisecond * 100)

		req := &milvuspb.DescribeIndexRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kDescribeIndex,
				MsgID:     210,
				Timestamp: 210,
				SourceID:  210,
			},
			DbName:         "",
			CollectionName: "testColl",
			FieldName:      "vector",
			IndexName:      "",
		}
		rsp, err := cli.DescribeIndex(req)
		assert.Nil(t, err)
		assert.Equal(t, rsp.Status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, len(rsp.IndexDescriptions), 2)
		assert.Equal(t, rsp.IndexDescriptions[0].IndexName, cms.Params.DefaultIndexName)
		assert.Equal(t, rsp.IndexDescriptions[1].IndexName, "index_field_100_0")

	})

	t.Run("drop partition", func(t *testing.T) {
		req := &milvuspb.DropPartitionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kDropPartition,
				MsgID:     220,
				Timestamp: 220,
				SourceID:  220,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
			PartitionName:  "testPartition",
		}
		status, err := cli.DropPartition(req)
		assert.Nil(t, err)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		collMeta, err := core.MetaTable.GetCollectionByName("testColl")
		assert.Nil(t, err)
		assert.Equal(t, len(collMeta.PartitionIDs), 1)
		partMeta, err := core.MetaTable.GetPartitionByID(collMeta.PartitionIDs[0])
		assert.Nil(t, err)
		assert.Equal(t, partMeta.PartitionName, cms.Params.DefaultPartitionName)
	})

	t.Run("drop collection", func(t *testing.T) {
		req := &milvuspb.DropCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kDropCollection,
				MsgID:     230,
				Timestamp: 230,
				SourceID:  230,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
		}

		status, err := cli.DropCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, len(dropCollectionArray), 1)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_SUCCESS)
		assert.Equal(t, dropCollectionArray[0].Base.MsgType, commonpb.MsgType_kDropCollection)
		assert.Equal(t, dropCollectionArray[0].CollectionName, "testColl")
		assert.Equal(t, len(collectionMetaCache), 1)
		assert.Equal(t, collectionMetaCache[0], "testColl")

		req = &milvuspb.DropCollectionRequest{
			Base: &commonpb.MsgBase{
				MsgType:   commonpb.MsgType_kDropCollection,
				MsgID:     231,
				Timestamp: 231,
				SourceID:  231,
			},
			DbName:         "testDb",
			CollectionName: "testColl",
		}
		status, err = cli.DropCollection(req)
		assert.Nil(t, err)
		assert.Equal(t, len(dropCollectionArray), 1)
		assert.Equal(t, status.ErrorCode, commonpb.ErrorCode_UNEXPECTED_ERROR)
	})

	err = cli.Stop()
	assert.Nil(t, err)

	err = svr.Stop()
	assert.Nil(t, err)
}