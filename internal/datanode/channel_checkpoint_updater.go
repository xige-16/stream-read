// Licensed to the LF AI & Data foundation under one
// or more contributor license agreements. See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership. The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License. You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package datanode

import (
	"context"
	"sync"
	"time"

	"github.com/samber/lo"
	"go.uber.org/zap"

	"github.com/milvus-io/milvus-proto/go-api/v2/msgpb"
	"github.com/milvus-io/milvus/pkg/log"
	"github.com/milvus-io/milvus/pkg/util/paramtable"
	"github.com/milvus-io/milvus/pkg/util/typeutil"
)

const (
	defaultUpdateChanCPMaxParallel = 10
)

type channelCPUpdateTask struct {
	pos      *msgpb.MsgPosition
	callback func()
	flush    bool
}

type channelCheckpointUpdater struct {
	dn *DataNode

	mu         sync.RWMutex
	tasks      map[string]*channelCPUpdateTask
	notifyChan chan struct{}

	closeCh   chan struct{}
	closeOnce sync.Once
}

func newChannelCheckpointUpdater(dn *DataNode) *channelCheckpointUpdater {
	return &channelCheckpointUpdater{
		dn:         dn,
		tasks:      make(map[string]*channelCPUpdateTask),
		closeCh:    make(chan struct{}),
		notifyChan: make(chan struct{}, 1),
	}
}

func (ccu *channelCheckpointUpdater) start() {
	log.Info("channel checkpoint updater start")
	ticker := time.NewTicker(paramtable.Get().DataNodeCfg.ChannelCheckpointUpdateTickInSeconds.GetAsDuration(time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-ccu.closeCh:
			log.Info("channel checkpoint updater exit")
			return
		case <-ccu.notifyChan:
			var tasks []*channelCPUpdateTask
			ccu.mu.Lock()
			for _, task := range ccu.tasks {
				if task.flush {
					task.flush = false
					tasks = append(tasks, task)
				}
			}
			ccu.mu.Unlock()
			if len(tasks) > 0 {
				ccu.updateCheckpoints(tasks)
			}
		case <-ticker.C:
			ccu.execute()
		}
	}
}

func (ccu *channelCheckpointUpdater) getTask(channel string) (*channelCPUpdateTask, bool) {
	ccu.mu.RLock()
	defer ccu.mu.RUnlock()
	task, ok := ccu.tasks[channel]
	return task, ok
}

func (ccu *channelCheckpointUpdater) trigger() {
	select {
	case ccu.notifyChan <- struct{}{}:
	default:
	}
}

func (ccu *channelCheckpointUpdater) updateCheckpoints(tasks []*channelCPUpdateTask) {
	taskGroups := lo.Chunk(tasks, paramtable.Get().DataNodeCfg.MaxChannelCheckpointsPerRPC.GetAsInt())
	updateChanCPMaxParallel := paramtable.Get().DataNodeCfg.UpdateChannelCheckpointMaxParallel.GetAsInt()
	if updateChanCPMaxParallel <= 0 {
		updateChanCPMaxParallel = defaultUpdateChanCPMaxParallel
	}
	rpcGroups := lo.Chunk(taskGroups, updateChanCPMaxParallel)

	finished := typeutil.NewConcurrentMap[string, *channelCPUpdateTask]()

	for _, groups := range rpcGroups {
		wg := &sync.WaitGroup{}
		for _, tasks := range groups {
			wg.Add(1)
			go func(tasks []*channelCPUpdateTask) {
				defer wg.Done()
				timeout := paramtable.Get().DataNodeCfg.UpdateChannelCheckpointRPCTimeout.GetAsDuration(time.Second)
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				channelCPs := lo.Map(tasks, func(t *channelCPUpdateTask, _ int) *msgpb.MsgPosition {
					return t.pos
				})
				err := ccu.dn.broker.UpdateChannelCheckpoint(ctx, channelCPs)
				if err != nil {
					log.Warn("update channel checkpoint failed", zap.Error(err))
					return
				}
				for _, task := range tasks {
					task.callback()
					finished.Insert(task.pos.GetChannelName(), task)
				}
			}(tasks)
		}
		wg.Wait()
	}

	ccu.mu.Lock()
	defer ccu.mu.Unlock()
	finished.Range(func(_ string, task *channelCPUpdateTask) bool {
		channel := task.pos.GetChannelName()
		if ccu.tasks[channel].pos.GetTimestamp() <= task.pos.GetTimestamp() {
			delete(ccu.tasks, channel)
		}
		return true
	})
}

func (ccu *channelCheckpointUpdater) execute() {
	ccu.mu.RLock()
	tasks := lo.Values(ccu.tasks)
	ccu.mu.RUnlock()

	ccu.updateCheckpoints(tasks)
}

func (ccu *channelCheckpointUpdater) AddTask(channelPos *msgpb.MsgPosition, flush bool, callback func()) {
	if channelPos == nil || channelPos.GetMsgID() == nil || channelPos.GetChannelName() == "" {
		log.Warn("illegal checkpoint", zap.Any("pos", channelPos))
		return
	}
	if flush {
		defer ccu.trigger()
	}
	channel := channelPos.GetChannelName()
	task, ok := ccu.getTask(channelPos.GetChannelName())
	if !ok {
		ccu.mu.Lock()
		defer ccu.mu.Unlock()
		ccu.tasks[channel] = &channelCPUpdateTask{
			pos:      channelPos,
			callback: callback,
			flush:    flush,
		}
		return
	}

	max := func(a, b *msgpb.MsgPosition) *msgpb.MsgPosition {
		if a.GetTimestamp() > b.GetTimestamp() {
			return a
		}
		return b
	}
	if task.pos.GetTimestamp() < channelPos.GetTimestamp() || (flush && !task.flush) {
		ccu.mu.Lock()
		defer ccu.mu.Unlock()
		ccu.tasks[channel] = &channelCPUpdateTask{
			pos:      max(channelPos, task.pos),
			callback: callback,
			flush:    flush || task.flush,
		}
	}
}

func (ccu *channelCheckpointUpdater) taskNum() int {
	ccu.mu.RLock()
	defer ccu.mu.RUnlock()
	return len(ccu.tasks)
}

func (ccu *channelCheckpointUpdater) close() {
	ccu.closeOnce.Do(func() {
		close(ccu.closeCh)
	})
}
