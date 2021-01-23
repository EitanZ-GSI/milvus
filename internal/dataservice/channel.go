package dataservice

import (
	"fmt"
	"strconv"
	"sync"
)

type (
	channelGroup         []string
	insertChannelManager struct {
		mu            sync.RWMutex
		count         int
		channelGroups map[UniqueID][]channelGroup // collection id to channel ranges
	}
)

func (cr channelGroup) Contains(channelName string) bool {
	for _, name := range cr {
		if name == channelName {
			return true
		}
	}
	return false
}

func newInsertChannelManager() *insertChannelManager {
	return &insertChannelManager{
		count:         0,
		channelGroups: make(map[UniqueID][]channelGroup),
	}
}

func (cm *insertChannelManager) AllocChannels(collectionID UniqueID, groupNum int) ([]channelGroup, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if _, ok := cm.channelGroups[collectionID]; ok {
		return nil, fmt.Errorf("channel group of collection %d already exist", collectionID)
	}
	channels := Params.InsertChannelNumPerCollection
	m, n := channels/int64(groupNum), channels%int64(groupNum)
	cg := make([]channelGroup, 0)
	var i, j int64 = 0, 0
	for i < channels {
		var group []string
		if j < n {
			group = make([]string, m+1)
		} else {
			group = make([]string, m)
		}
		for k := 0; k < len(group); k++ {
			group = append(group, Params.InsertChannelPrefixName+strconv.Itoa(cm.count))
			cm.count++
		}
		i += int64(len(group))
		j++
		cg = append(cg, group)
	}
	return cg, nil
}

func (cm *insertChannelManager) GetChannelGroup(collectionID UniqueID, channelName string) (channelGroup, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	_, ok := cm.channelGroups[collectionID]
	if !ok {
		return nil, fmt.Errorf("can not find collection %d", collectionID)
	}
	for _, cr := range cm.channelGroups[collectionID] {
		if cr.Contains(channelName) {
			return cr, nil
		}
	}
	return nil, fmt.Errorf("channel name %s not found", channelName)
}

func (cm *insertChannelManager) ContainsCollection(collectionID UniqueID) (bool, []string) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	_, ok := cm.channelGroups[collectionID]
	if !ok {
		return false, nil
	}
	ret := make([]string, 0)
	for _, cr := range cm.channelGroups[collectionID] {
		for _, c := range cr {
			ret = append(ret, c)
		}
	}
	return true, ret
}