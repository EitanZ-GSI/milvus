// Copyright (C) 2019-2020 Zilliz. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance
// with the License. You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License
// is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express
// or implied. See the License for the specific language governing permissions and limitations under the License.

package masterservice

import (
	"fmt"
	"sync"

	"github.com/milvus-io/milvus/internal/log"
	"github.com/milvus-io/milvus/internal/msgstream"
	"github.com/milvus-io/milvus/internal/proto/commonpb"
	"github.com/milvus-io/milvus/internal/proto/internalpb"
	"github.com/milvus-io/milvus/internal/util/sessionutil"
	"github.com/milvus-io/milvus/internal/util/typeutil"
	"go.uber.org/zap"
)

type timetickSync struct {
	core          *Core
	lock          sync.Mutex
	proxyTimeTick map[typeutil.UniqueID]*internalpb.ChannelTimeTickMsg
	chanStream    map[string]msgstream.MsgStream
	sendChan      chan map[typeutil.UniqueID]*internalpb.ChannelTimeTickMsg
}

func newTimeTickSync(core *Core) *timetickSync {
	return &timetickSync{
		lock:          sync.Mutex{},
		core:          core,
		proxyTimeTick: make(map[typeutil.UniqueID]*internalpb.ChannelTimeTickMsg),
		chanStream:    make(map[string]msgstream.MsgStream),
		sendChan:      make(chan map[typeutil.UniqueID]*internalpb.ChannelTimeTickMsg, 16),
	}
}

// sendToChannel send all channels' timetick to sendChan
// lock is needed by the invoker
func (t *timetickSync) sendToChannel() {
	for _, v := range t.proxyTimeTick {
		if v == nil {
			return
		}
	}
	if len(t.proxyTimeTick) == 0 {
		return
	}
	// clear proxyTimeTick and send a clone
	ptt := make(map[typeutil.UniqueID]*internalpb.ChannelTimeTickMsg)
	for k, v := range t.proxyTimeTick {
		ptt[k] = v
		t.proxyTimeTick[k] = nil
	}
	t.sendChan <- ptt
}

// UpdateTimeTick check msg validation and send it to local channel
func (t *timetickSync) UpdateTimeTick(in *internalpb.ChannelTimeTickMsg) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	_, ok := t.proxyTimeTick[in.Base.SourceID]
	if !ok {
		return fmt.Errorf("Skip ChannelTimeTickMsg from un-recognized proxy node %d", in.Base.SourceID)
	}
	t.proxyTimeTick[in.Base.SourceID] = in
	t.sendToChannel()
	return nil
}

func (t *timetickSync) AddProxyNode(sess *sessionutil.Session) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.proxyTimeTick[sess.ServerID] = nil
}

func (t *timetickSync) DelProxyNode(sess *sessionutil.Session) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if _, ok := t.proxyTimeTick[sess.ServerID]; ok {
		delete(t.proxyTimeTick, sess.ServerID)
		t.sendToChannel()
	}
}

func (t *timetickSync) GetProxyNodes(sess []*sessionutil.Session) {
	t.lock.Lock()
	defer t.lock.Unlock()
	for _, s := range sess {
		t.proxyTimeTick[s.ServerID] = nil
	}
}

// StartWatch watch proxy node change and process all channels' timetick msg
func (t *timetickSync) StartWatch() {
	for {
		select {
		case <-t.core.ctx.Done():
			log.Debug("master service context done", zap.Error(t.core.ctx.Err()))
			return
		case ptt, ok := <-t.sendChan:
			if !ok {
				log.Debug("timetickSync sendChan closed")
				return
			}
			// reduce each channel to get min timestamp
			chanName2TimeTickMap := make(map[string]typeutil.Timestamp)
			for _, cttMsg := range ptt {
				chanNum := len(cttMsg.ChannelNames)
				for i := 0; i < chanNum; i++ {
					name := cttMsg.ChannelNames[i]
					ts := cttMsg.Timestamps[i]
					cts, ok := chanName2TimeTickMap[name]
					if !ok || ts < cts {
						chanName2TimeTickMap[name] = ts
					}
				}
			}
			// send timetick msg to msg stream
			for chanName, chanTs := range chanName2TimeTickMap {
				if err := t.SendChannelTimeTick(chanName, chanTs); err != nil {
					log.Debug("SendChannelTimeTick fail", zap.Error(err))
				}
			}
		}
	}
}

// SendChannelTimeTick send each channel's min timetick to msg stream
func (t *timetickSync) SendChannelTimeTick(chanName string, ts typeutil.Timestamp) error {
	msgPack := msgstream.MsgPack{}
	baseMsg := msgstream.BaseMsg{
		BeginTimestamp: ts,
		EndTimestamp:   ts,
		HashValues:     []uint32{0},
	}
	timeTickResult := internalpb.TimeTickMsg{
		Base: &commonpb.MsgBase{
			MsgType:   commonpb.MsgType_TimeTick,
			MsgID:     0,
			Timestamp: ts,
			SourceID:  t.core.session.ServerID,
		},
	}
	timeTickMsg := &msgstream.TimeTickMsg{
		BaseMsg:     baseMsg,
		TimeTickMsg: timeTickResult,
	}
	msgPack.Msgs = append(msgPack.Msgs, timeTickMsg)

	t.lock.Lock()
	defer t.lock.Unlock()

	// send timetick msg to msg stream
	var err error
	var stream msgstream.MsgStream
	stream, ok := t.chanStream[chanName]
	if !ok {
		stream, err = t.core.msFactory.NewMsgStream(t.core.ctx)
		if err != nil {
			return err
		}
		stream.AsProducer([]string{chanName})
		t.chanStream[chanName] = stream
	}
	return stream.Broadcast(&msgPack)
}

// GetProxyNodeNum return the num of detected proxy node
func (t *timetickSync) GetProxyNodeNum() int {
	t.lock.Lock()
	defer t.lock.Unlock()
	return len(t.proxyTimeTick)
}

// GetChanNum return the num of channel
func (t *timetickSync) GetChanNum() int {
	t.lock.Lock()
	defer t.lock.Unlock()
	return len(t.chanStream)
}