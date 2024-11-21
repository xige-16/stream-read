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
	"fmt"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/samber/lo"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/msgpb"
	"github.com/milvus-io/milvus/internal/datanode/allocator"
	"github.com/milvus-io/milvus/internal/proto/datapb"
	"github.com/milvus-io/milvus/internal/proto/etcdpb"
	"github.com/milvus-io/milvus/internal/storage"
	"github.com/milvus-io/milvus/pkg/common"
	"github.com/milvus-io/milvus/pkg/log"
	"github.com/milvus-io/milvus/pkg/metrics"
	"github.com/milvus-io/milvus/pkg/util/commonpbutil"
	"github.com/milvus-io/milvus/pkg/util/merr"
	"github.com/milvus-io/milvus/pkg/util/metautil"
	"github.com/milvus-io/milvus/pkg/util/paramtable"
	"github.com/milvus-io/milvus/pkg/util/retry"
	"github.com/milvus-io/milvus/pkg/util/timerecord"
	"github.com/milvus-io/milvus/pkg/util/typeutil"
)

// flushManager defines a flush manager signature
type flushManager interface {
	// notify flush manager insert buffer data
	flushBufferData(data *BufferData, segmentID UniqueID, flushed bool, dropped bool, pos *msgpb.MsgPosition) (*storage.PrimaryKeyStats, error)
	// notify flush manager del buffer data
	flushDelData(data *DelDataBuf, segmentID UniqueID, pos *msgpb.MsgPosition) error
	// isFull return true if the task pool is full
	isFull() bool
	// injectFlush injects compaction or other blocking task before flush sync
	injectFlush(injection *taskInjection, segments ...UniqueID)
	// startDropping changes flush manager into dropping mode
	startDropping()
	// notifyAllFlushed tells flush manager there is not future incoming flush task for drop mode
	notifyAllFlushed()
	// close handles resource clean up
	close()
	start()
}

// segmentFlushPack contains result to save into meta
type segmentFlushPack struct {
	segmentID  UniqueID
	insertLogs map[UniqueID]*datapb.Binlog
	statsLogs  map[UniqueID]*datapb.Binlog
	deltaLogs  []*datapb.Binlog
	pos        *msgpb.MsgPosition
	flushed    bool
	dropped    bool
	err        error // task execution error, if not nil, notify func should stop datanode
}

// notifyMetaFunc notify meta to persistent flush result
type notifyMetaFunc func(*segmentFlushPack)

// flushAndDropFunc notifies meta to flush current state and drop virtual channel
type flushAndDropFunc func([]*segmentFlushPack)

// taskPostFunc clean up function after single flush task done
type taskPostFunc func(pack *segmentFlushPack, postInjection postInjectionFunc, isFlush bool)

// postInjectionFunc post injection pack process logic
type postInjectionFunc func(pack *segmentFlushPack)

// make sure implementation
var _ flushManager = (*rendezvousFlushManager)(nil)

// orderFlushQueue keeps the order of task notifyFunc execution in order
type orderFlushQueue struct {
	sync.Once
	segmentID UniqueID

	// MsgID => flushTask
	working    *typeutil.ConcurrentMap[string, *flushTaskRunner]
	notifyFunc notifyMetaFunc

	// protect postInjection
	injectMut     sync.Mutex
	injectCh      chan *taskInjection
	postInjection postInjectionFunc

	// protect task create and close
	taskMut      sync.Mutex
	runningTasks int32
	tailCh       chan struct{}
}

// newOrderFlushQueue creates an orderFlushQueue
func newOrderFlushQueue(segID UniqueID, f notifyMetaFunc) *orderFlushQueue {
	q := &orderFlushQueue{
		segmentID:  segID,
		notifyFunc: f,
		injectCh:   make(chan *taskInjection, 100),
		working:    typeutil.NewConcurrentMap[string, *flushTaskRunner](),
	}
	return q
}

// init orderFlushQueue use once protect init, init tailCh
func (q *orderFlushQueue) init() {
	q.Once.Do(func() {
		// new queue acts like tailing task is done
		q.tailCh = make(chan struct{})
		close(q.tailCh)
	})
}

func (q *orderFlushQueue) getFlushTask(pos *msgpb.MsgPosition) *flushTaskRunner {
	t, loaded := q.working.GetOrInsert(getSyncTaskID(pos), newFlushTaskRunner(q.segmentID, q.injectCh))
	// not loaded means the task runner is new, do initializtion
	if !loaded {
		q.taskMut.Lock()
		defer q.taskMut.Unlock()

		q.runningTasks++
		t.init(q.notifyFunc, q.postTask, q.tailCh)
		q.tailCh = t.finishSignal
		log.Info("new flush task runner created and initialized",
			zap.Int64("segmentID", q.segmentID),
			zap.String("pos message ID", string(pos.GetMsgID())),
		)
	}
	return t
}

// postTask handles clean up work after a task is done
func (q *orderFlushQueue) postTask(pack *segmentFlushPack, postInjection postInjectionFunc, isFlush bool) {
	// delete flush task from working map
	if isFlush {
		q.working.GetAndRemove(getSyncTaskID(pack.pos))
	}

	q.injectMut.Lock()
	// set postInjection function if injection is handled in task
	if postInjection != nil {
		q.postInjection = postInjection
	}

	if isFlush && q.postInjection != nil {
		q.postInjection(pack)
	}
	q.injectMut.Unlock()

	// after descreasing working count, check whether flush queue is empty
	q.taskMut.Lock()
	q.runningTasks--
	if q.runningTasks == 0 && len(q.injectCh) > 0 {
		q.enqueueInjectTask()
	}

	q.taskMut.Unlock()
}

// enqueueInsertBuffer put insert buffer data into queue
func (q *orderFlushQueue) enqueueInsertFlush(task flushInsertTask, binlogs, statslogs map[UniqueID]*datapb.Binlog, flushed bool, dropped bool, pos *msgpb.MsgPosition) {
	q.getFlushTask(pos).runFlushInsert(task, binlogs, statslogs, flushed, dropped, pos)
}

// enqueueDelBuffer put delete buffer data into queue
func (q *orderFlushQueue) enqueueDelFlush(task flushDeleteTask, deltaLogs *DelDataBuf, pos *msgpb.MsgPosition) {
	q.getFlushTask(pos).runFlushDel(task, deltaLogs)
}

// create inject task to drain all injection from injectCh
// WARN: RUN WITH TASK MUT
func (q *orderFlushQueue) enqueueInjectTask() {
	q.runningTasks++
	t := newInjectTask(q.postTask, q.injectCh, q.tailCh)
	q.tailCh = t.finishSignal

	log.Info("new injection task enqueue",
		zap.Int64("segmentID", q.segmentID),
	)
}

// inject performs injection for current task queue
// send into injectCh in there is running task
// or perform injection logic here if there is no injection
func (q *orderFlushQueue) inject(inject *taskInjection) {
	q.injectCh <- inject

	q.taskMut.Lock()
	defer q.taskMut.Unlock()

	if q.runningTasks == 0 {
		q.enqueueInjectTask()
	}
}

func (q *orderFlushQueue) getTailChan() chan struct{} {
	q.taskMut.Lock()
	defer q.taskMut.Unlock()

	return q.tailCh
}

func (q *orderFlushQueue) checkEmpty() bool {
	if q.taskMut.TryLock() {
		defer q.taskMut.Unlock()
		return q.runningTasks == 0
	}
	return false
}

// injectTask handles injection for empty flush queue
type injectTask struct {
	startSignal, finishSignal chan struct{}
	injectCh                  <-chan *taskInjection
	postFunc                  taskPostFunc
}

// newInjectTask create injection task to flush queue
func newInjectTask(postFunc taskPostFunc, injectCh chan *taskInjection, startSignal chan struct{}) *injectTask {
	h := &injectTask{
		postFunc:     postFunc,
		injectCh:     injectCh,
		startSignal:  startSignal,
		finishSignal: make(chan struct{}),
	}

	go h.waitFinish()
	return h
}

func (h *injectTask) waitFinish() {
	<-h.startSignal
	var postInjection postInjectionFunc
	select {
	case injection := <-h.injectCh:
		// notify injected
		injection.injectOne()
		ok := <-injection.injectOver
		if ok {
			// apply postInjection func
			postInjection = injection.postInjection
		}
	default:
	}

	h.postFunc(nil, postInjection, false)
	// notify next task
	close(h.finishSignal)
}

type dropHandler struct {
	sync.Mutex
	dropFlushWg  sync.WaitGroup
	flushAndDrop flushAndDropFunc
	allFlushed   chan struct{}
	packs        []*segmentFlushPack
}

// rendezvousFlushManager makes sure insert & del buf all flushed
type rendezvousFlushManager struct {
	allocator.Allocator
	storage.ChunkManager
	Channel

	// segment id => flush queue
	dispatcher *typeutil.ConcurrentMap[int64, *orderFlushQueue]
	notifyFunc notifyMetaFunc

	dropping    atomic.Bool
	dropHandler dropHandler
	ctx         context.Context
	cancel      context.CancelFunc
	cleanLock   sync.RWMutex
	wg          sync.WaitGroup
}

// start the cleanLoop
func (m *rendezvousFlushManager) start() {
	m.wg.Add(1)
	go m.cleanLoop()
}

// getFlushQueue gets or creates an orderFlushQueue for segment id if not found
func (m *rendezvousFlushManager) getFlushQueue(segmentID UniqueID) *orderFlushQueue {
	newQueue := newOrderFlushQueue(segmentID, m.notifyFunc)
	queue, _ := m.dispatcher.GetOrInsert(segmentID, newQueue)
	queue.init()
	return queue
}

func (m *rendezvousFlushManager) handleInsertTask(segmentID UniqueID, task flushInsertTask, binlogs, statslogs map[UniqueID]*datapb.Binlog, flushed bool, dropped bool, pos *msgpb.MsgPosition) {
	log.Info("handling insert task",
		zap.Int64("segmentID", segmentID),
		zap.Bool("flushed", flushed),
		zap.Bool("dropped", dropped),
		zap.Any("position", pos),
	)
	// in dropping mode
	if m.dropping.Load() {
		r := &flushTaskRunner{
			WaitGroup: sync.WaitGroup{},
			segmentID: segmentID,
		}
		r.WaitGroup.Add(1) // insert and delete are not bound in drop mode
		r.runFlushInsert(task, binlogs, statslogs, flushed, dropped, pos)
		r.WaitGroup.Wait()

		m.dropHandler.Lock()
		defer m.dropHandler.Unlock()
		m.dropHandler.packs = append(m.dropHandler.packs, r.getFlushPack())

		return
	}
	// normal mode
	m.cleanLock.RLock()
	defer m.cleanLock.RUnlock()
	m.getFlushQueue(segmentID).enqueueInsertFlush(task, binlogs, statslogs, flushed, dropped, pos)
}

func (m *rendezvousFlushManager) handleDeleteTask(segmentID UniqueID, task flushDeleteTask, deltaLogs *DelDataBuf, pos *msgpb.MsgPosition) {
	log.Info("handling delete task", zap.Int64("segmentID", segmentID))
	// in dropping mode
	if m.dropping.Load() {
		// preventing separate delete, check position exists in queue first
		m.cleanLock.RLock()
		q := m.getFlushQueue(segmentID)
		_, ok := q.working.Get(getSyncTaskID(pos))
		m.cleanLock.RUnlock()
		// if ok, means position insert data already in queue, just handle task in normal mode
		// if not ok, means the insert buf should be handle in drop mode
		if !ok {
			r := &flushTaskRunner{
				WaitGroup: sync.WaitGroup{},
				segmentID: segmentID,
			}
			r.WaitGroup.Add(1) // insert and delete are not bound in drop mode
			r.runFlushDel(task, deltaLogs)
			r.WaitGroup.Wait()

			m.dropHandler.Lock()
			defer m.dropHandler.Unlock()
			m.dropHandler.packs = append(m.dropHandler.packs, r.getFlushPack())
			return
		}
	}
	// normal mode
	m.cleanLock.RLock()
	defer m.cleanLock.RUnlock()
	m.getFlushQueue(segmentID).enqueueDelFlush(task, deltaLogs, pos)
}

func (m *rendezvousFlushManager) serializeBinLog(segmentID, partID int64, data *BufferData, inCodec *storage.InsertCodec) ([]*Blob, map[int64]int, error) {
	fieldMemorySize := make(map[int64]int)

	if data == nil || data.buffer == nil {
		return []*Blob{}, fieldMemorySize, nil
	}

	// get memory size of buffer data
	for fieldID, fieldData := range data.buffer.Data {
		fieldMemorySize[fieldID] = fieldData.GetMemorySize()
	}

	// encode data and convert output data
	blobs, err := inCodec.Serialize(partID, segmentID, data.buffer)
	if err != nil {
		return nil, nil, err
	}
	return blobs, fieldMemorySize, nil
}

func (m *rendezvousFlushManager) serializePkStatsLog(segmentID int64, flushed bool, data *BufferData, inCodec *storage.InsertCodec) (*Blob, *storage.PrimaryKeyStats, error) {
	var err error
	var stats *storage.PrimaryKeyStats

	pkField := getPKField(inCodec.Schema)
	if pkField == nil {
		log.Error("No pk field in schema", zap.Int64("segmentID", segmentID), zap.Int64("collectionID", inCodec.Schema.GetID()))
		return nil, nil, fmt.Errorf("no primary key in meta")
	}

	var insertData storage.FieldData
	rowNum := int64(0)
	if data != nil && data.buffer != nil {
		insertData = data.buffer.Data[pkField.FieldID]
		rowNum = int64(insertData.RowNum())
		if insertData.RowNum() > 0 {
			// gen stats of buffer insert data
			stats = storage.NewPrimaryKeyStats(pkField.FieldID, int64(pkField.DataType), rowNum)
			stats.UpdateByMsgs(insertData)
		}
	}

	// get all stats log as a list, serialize to blob
	// if flushed
	if flushed {
		seg := m.getSegment(segmentID)
		if seg == nil {
			return nil, nil, merr.WrapErrSegmentNotFound(segmentID)
		}

		statsList, oldRowNum := seg.getHistoricalStats(pkField)
		if stats != nil {
			statsList = append(statsList, stats)
		}

		blob, err := inCodec.SerializePkStatsList(statsList, oldRowNum+rowNum)
		if err != nil {
			return nil, nil, err
		}
		return blob, stats, nil
	}

	if rowNum == 0 {
		return nil, nil, nil
	}

	// only serialize stats gen from new insert data
	// if not flush
	blob, err := inCodec.SerializePkStats(stats, rowNum)
	if err != nil {
		return nil, nil, err
	}

	return blob, stats, nil
}

// isFull return true if the task pool is full
func (m *rendezvousFlushManager) isFull() bool {
	var num int
	m.dispatcher.Range(func(_ int64, queue *orderFlushQueue) bool {
		num += queue.working.Len()
		return true
	})
	return num >= Params.DataNodeCfg.MaxParallelSyncTaskNum.GetAsInt()
}

// flushBufferData notifies flush manager insert buffer data.
// This method will be retired on errors. Final errors will be propagated upstream and logged.
func (m *rendezvousFlushManager) flushBufferData(data *BufferData, segmentID UniqueID, flushed bool, dropped bool, pos *msgpb.MsgPosition) (*storage.PrimaryKeyStats, error) {
	field2Insert := make(map[UniqueID]*datapb.Binlog)
	field2Stats := make(map[UniqueID]*datapb.Binlog)
	kvs := make(map[string][]byte)

	tr := timerecord.NewTimeRecorder("flushDuration")
	// get segment info
	collID, partID, meta, err := m.getSegmentMeta(segmentID, pos)
	if err != nil {
		return nil, err
	}
	inCodec := storage.NewInsertCodecWithSchema(meta)
	// build bin log blob
	binLogBlobs, fieldMemorySize, err := m.serializeBinLog(segmentID, partID, data, inCodec)
	if err != nil {
		return nil, err
	}

	// build stats log blob
	pkStatsBlob, stats, err := m.serializePkStatsLog(segmentID, flushed, data, inCodec)
	if err != nil {
		return nil, err
	}

	// allocate
	// alloc for stats log if have new stats log and not flushing
	var logidx int64
	allocNum := uint32(len(binLogBlobs) + boolToInt(!flushed && pkStatsBlob != nil))
	if allocNum != 0 {
		logidx, _, err = m.Alloc(allocNum)
		if err != nil {
			return nil, err
		}
	}

	// binlogs
	for _, blob := range binLogBlobs {
		fieldID, err := strconv.ParseInt(blob.GetKey(), 10, 64)
		if err != nil {
			log.Error("Flush failed ... cannot parse string to fieldID ..", zap.Error(err))
			return nil, err
		}

		k := metautil.JoinIDPath(collID, partID, segmentID, fieldID, logidx)
		// [rootPath]/[insert_log]/key
		key := path.Join(m.ChunkManager.RootPath(), common.SegmentInsertLogPath, k)
		kvs[key] = blob.Value[:]
		field2Insert[fieldID] = &datapb.Binlog{
			EntriesNum:    data.size,
			TimestampFrom: data.tsFrom,
			TimestampTo:   data.tsTo,
			LogPath:       key,
			LogSize:       int64(fieldMemorySize[fieldID]),
		}

		logidx += 1
	}

	// pk stats binlog
	if pkStatsBlob != nil {
		fieldID, err := strconv.ParseInt(pkStatsBlob.GetKey(), 10, 64)
		if err != nil {
			log.Error("Flush failed ... cannot parse string to fieldID ..", zap.Error(err))
			return nil, err
		}

		// use storage.FlushedStatsLogIdx as logidx if flushed
		// else use last idx we allocated
		var key string
		if flushed {
			k := metautil.JoinIDPath(collID, partID, segmentID, fieldID)
			key = path.Join(m.ChunkManager.RootPath(), common.SegmentStatslogPath, k, storage.CompoundStatsType.LogIdx())
		} else {
			k := metautil.JoinIDPath(collID, partID, segmentID, fieldID, logidx)
			key = path.Join(m.ChunkManager.RootPath(), common.SegmentStatslogPath, k)
		}

		kvs[key] = pkStatsBlob.Value
		field2Stats[fieldID] = &datapb.Binlog{
			EntriesNum:    0,
			TimestampFrom: 0, // TODO
			TimestampTo:   0, // TODO,
			LogPath:       key,
			LogSize:       int64(len(pkStatsBlob.Value)),
		}
	}

	m.handleInsertTask(segmentID, &flushBufferInsertTask{
		ChunkManager: m.ChunkManager,
		data:         kvs,
	}, field2Insert, field2Stats, flushed, dropped, pos)

	metrics.DataNodeEncodeBufferLatency.WithLabelValues(fmt.Sprint(paramtable.GetNodeID())).Observe(float64(tr.ElapseSpan().Milliseconds()))
	return stats, nil
}

// notify flush manager del buffer data
func (m *rendezvousFlushManager) flushDelData(data *DelDataBuf, segmentID UniqueID,
	pos *msgpb.MsgPosition,
) error {
	// del signal with empty data
	if data == nil || data.delData == nil {
		m.handleDeleteTask(segmentID, &flushBufferDeleteTask{}, nil, pos)
		return nil
	}

	collID, partID, err := m.getCollectionAndPartitionID(segmentID)
	if err != nil {
		return err
	}

	delCodec := storage.NewDeleteCodec()

	blob, err := delCodec.Serialize(collID, partID, segmentID, data.delData)
	if err != nil {
		return err
	}

	logID, err := m.AllocOne()
	if err != nil {
		log.Error("failed to alloc ID", zap.Error(err))
		return err
	}

	blobKey := metautil.JoinIDPath(collID, partID, segmentID, logID)
	blobPath := path.Join(m.ChunkManager.RootPath(), common.SegmentDeltaLogPath, blobKey)
	kvs := map[string][]byte{blobPath: blob.Value[:]}
	data.LogSize = int64(len(blob.Value))
	data.LogPath = blobPath
	log.Info("delete blob path", zap.String("path", blobPath))
	m.handleDeleteTask(segmentID, &flushBufferDeleteTask{
		ChunkManager: m.ChunkManager,
		data:         kvs,
	}, data, pos)
	return nil
}

// injectFlush inject process before task finishes
func (m *rendezvousFlushManager) injectFlush(injection *taskInjection, segments ...UniqueID) {
	go injection.waitForInjected()
	m.cleanLock.RLock()
	defer m.cleanLock.RUnlock()
	for _, segmentID := range segments {
		m.getFlushQueue(segmentID).inject(injection)
	}
}

// tryRemoveFlushQueue try to remove queue which running task is zero
func (m *rendezvousFlushManager) tryRemoveFlushQueue() {
	m.cleanLock.Lock()
	defer m.cleanLock.Unlock()
	m.dispatcher.Range(func(segmentID int64, queue *orderFlushQueue) bool {
		if queue.checkEmpty() {
			m.dispatcher.Remove(segmentID)
		}
		return true
	})
}

// segmentNum return the number of segment in dispatcher
func (m *rendezvousFlushManager) segmentNum() int {
	return m.dispatcher.Len()
}

// cleanLoop calls tryRemoveFlushQueue periodically
func (m *rendezvousFlushManager) cleanLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(Params.DataNodeCfg.FlushMgrCleanInterval.GetAsDuration(time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			log.Info("rendezvousFlushManager quit clean loop")
			return

		case <-ticker.C:
			m.tryRemoveFlushQueue()
			ticker.Reset(Params.DataNodeCfg.FlushMgrCleanInterval.GetAsDuration(time.Second))
		}
	}
}

// fetch meta info for segment
func (m *rendezvousFlushManager) getSegmentMeta(segmentID UniqueID, pos *msgpb.MsgPosition) (UniqueID, UniqueID, *etcdpb.CollectionMeta, error) {
	if !m.hasSegment(segmentID, true) {
		return -1, -1, nil, merr.WrapErrSegmentNotFound(segmentID, "segment not found during flush")
	}

	// fetch meta information of segment
	collID, partID, err := m.getCollectionAndPartitionID(segmentID)
	if err != nil {
		return -1, -1, nil, err
	}
	sch, err := m.getCollectionSchema(collID, pos.GetTimestamp())
	if err != nil {
		return -1, -1, nil, err
	}

	meta := &etcdpb.CollectionMeta{
		ID:     collID,
		Schema: sch,
	}
	return collID, partID, meta, nil
}

// waitForAllTaskQueue waits for all flush queues in dispatcher become empty
func (m *rendezvousFlushManager) waitForAllFlushQueue() {
	var wg sync.WaitGroup
	m.dispatcher.Range(func(segmentID int64, queue *orderFlushQueue) bool {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-queue.getTailChan()
		}()
		return true
	})
	wg.Wait()
}

// startDropping changes flush manager into dropping mode
func (m *rendezvousFlushManager) startDropping() {
	m.dropping.Store(true)
	m.dropHandler.allFlushed = make(chan struct{})
	go func() {
		<-m.dropHandler.allFlushed       // all needed flush tasks are in flush manager now
		m.waitForAllFlushQueue()         // waits for all the normal flush queue done
		m.dropHandler.dropFlushWg.Wait() // waits for all drop mode task done
		m.dropHandler.Lock()
		defer m.dropHandler.Unlock()
		// apply injection if any
		m.cleanLock.RLock()
		defer m.cleanLock.RUnlock()
		for _, pack := range m.dropHandler.packs {
			q := m.getFlushQueue(pack.segmentID)
			// queue will never be nil, sincde getFlushQueue will initialize one if not found
			q.injectMut.Lock()
			if q.postInjection != nil {
				q.postInjection(pack)
			}
			q.injectMut.Unlock()
		}
		m.dropHandler.flushAndDrop(m.dropHandler.packs) // invoke drop & flush
	}()
}

func (m *rendezvousFlushManager) notifyAllFlushed() {
	close(m.dropHandler.allFlushed)
}

func getSyncTaskID(pos *msgpb.MsgPosition) string {
	// use msgID & timestamp to generate unique taskID, see also #20926
	return fmt.Sprintf("%s%d", string(pos.GetMsgID()), pos.GetTimestamp())
}

// close cleans up all the left members
func (m *rendezvousFlushManager) close() {
	m.cancel()
	m.dispatcher.Range(func(segmentID int64, queue *orderFlushQueue) bool {
		// assertion ok
		queue.taskMut.Lock()
		queue.enqueueInjectTask()
		queue.taskMut.Unlock()
		return true
	})
	m.waitForAllFlushQueue()
	m.wg.Wait()
	log.Ctx(context.Background()).Info("flush manager closed", zap.Int64("collectionID", m.Channel.getCollectionID()))
}

type flushBufferInsertTask struct {
	storage.ChunkManager
	data map[string][]byte
}

// flushInsertData implements flushInsertTask
func (t *flushBufferInsertTask) flushInsertData() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if t.ChunkManager != nil && len(t.data) > 0 {
		tr := timerecord.NewTimeRecorder("insertData")
		group, ctx := errgroup.WithContext(ctx)
		for key, data := range t.data {
			key := key
			data := data
			group.Go(func() error {
				return t.Write(ctx, key, data)
			})
		}
		err := group.Wait()
		metrics.DataNodeSave2StorageLatency.WithLabelValues(fmt.Sprint(paramtable.GetNodeID()), metrics.InsertLabel).Observe(float64(tr.ElapseSpan().Milliseconds()))
		if err != nil {
			log.Warn("failed to flush insert data", zap.Error(err))
			return err
		}
		for _, d := range t.data {
			metrics.DataNodeFlushedSize.WithLabelValues(fmt.Sprint(paramtable.GetNodeID()), metrics.InsertLabel).Add(float64(len(d)))
		}
	}
	return nil
}

type flushBufferDeleteTask struct {
	storage.ChunkManager
	data map[string][]byte
}

// flushDeleteData implements flushDeleteTask
func (t *flushBufferDeleteTask) flushDeleteData() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if len(t.data) > 0 && t.ChunkManager != nil {
		tr := timerecord.NewTimeRecorder("deleteData")
		err := t.MultiWrite(ctx, t.data)
		metrics.DataNodeSave2StorageLatency.WithLabelValues(fmt.Sprint(paramtable.GetNodeID()), metrics.DeleteLabel).Observe(float64(tr.ElapseSpan().Milliseconds()))
		if err != nil {
			log.Warn("failed to flush delete data", zap.Error(err))
			return err
		}
		for _, d := range t.data {
			metrics.DataNodeFlushedSize.WithLabelValues(fmt.Sprint(paramtable.GetNodeID()), metrics.DeleteLabel).Add(float64(len(d)))
		}
	}
	return nil
}

// NewRendezvousFlushManager create rendezvousFlushManager with provided allocator and kv
func NewRendezvousFlushManager(allocator allocator.Allocator, cm storage.ChunkManager, channel Channel, f notifyMetaFunc, drop flushAndDropFunc) *rendezvousFlushManager {
	ctx, cancel := context.WithCancel(context.Background())
	fm := &rendezvousFlushManager{
		Allocator:    allocator,
		ChunkManager: cm,
		notifyFunc:   f,
		Channel:      channel,
		dropHandler: dropHandler{
			flushAndDrop: drop,
		},
		dispatcher: typeutil.NewConcurrentMap[int64, *orderFlushQueue](),
		ctx:        ctx,
		cancel:     cancel,
	}
	// start with normal mode
	fm.dropping.Store(false)
	return fm
}

func getFieldBinlogs(fieldID UniqueID, binlogs []*datapb.FieldBinlog) *datapb.FieldBinlog {
	for _, binlog := range binlogs {
		if fieldID == binlog.GetFieldID() {
			return binlog
		}
	}
	return nil
}

func dropVirtualChannelFunc(dsService *dataSyncService, opts ...retry.Option) flushAndDropFunc {
	return func(packs []*segmentFlushPack) {
		req := &datapb.DropVirtualChannelRequest{
			Base: commonpbutil.NewMsgBase(
				commonpbutil.WithMsgType(0), // TODO msg type
				commonpbutil.WithMsgID(0),   // TODO msg id
				commonpbutil.WithSourceID(dsService.serverID),
			),
			ChannelName: dsService.vchannelName,
		}

		segmentPack := make(map[UniqueID]*datapb.DropVirtualChannelSegment)
		for _, pack := range packs {
			segment, has := segmentPack[pack.segmentID]
			if !has {
				segment = &datapb.DropVirtualChannelSegment{
					SegmentID:    pack.segmentID,
					CollectionID: dsService.collectionID,
				}

				segmentPack[pack.segmentID] = segment
			}
			for k, v := range pack.insertLogs {
				fieldBinlogs := getFieldBinlogs(k, segment.Field2BinlogPaths)
				if fieldBinlogs == nil {
					segment.Field2BinlogPaths = append(segment.Field2BinlogPaths, &datapb.FieldBinlog{
						FieldID: k,
						Binlogs: []*datapb.Binlog{v},
					})
				} else {
					fieldBinlogs.Binlogs = append(fieldBinlogs.Binlogs, v)
				}
			}
			for k, v := range pack.statsLogs {
				fieldStatsLogs := getFieldBinlogs(k, segment.Field2StatslogPaths)
				if fieldStatsLogs == nil {
					segment.Field2StatslogPaths = append(segment.Field2StatslogPaths, &datapb.FieldBinlog{
						FieldID: k,
						Binlogs: []*datapb.Binlog{v},
					})
				} else {
					fieldStatsLogs.Binlogs = append(fieldStatsLogs.Binlogs, v)
				}
			}
			segment.Deltalogs = append(segment.Deltalogs, &datapb.FieldBinlog{
				Binlogs: pack.deltaLogs,
			})
			updates, _ := dsService.channel.getSegmentStatisticsUpdates(pack.segmentID)
			segment.NumOfRows = updates.GetNumRows()
			if pack.pos != nil {
				if segment.CheckPoint == nil || pack.pos.Timestamp > segment.CheckPoint.Timestamp {
					segment.CheckPoint = pack.pos
				}
			}
		}

		startPos := dsService.channel.listNewSegmentsStartPositions()
		// start positions for all new segments
		for _, pos := range startPos {
			segment, has := segmentPack[pos.GetSegmentID()]
			if !has {
				segment = &datapb.DropVirtualChannelSegment{
					SegmentID:    pos.GetSegmentID(),
					CollectionID: dsService.collectionID,
				}

				segmentPack[pos.GetSegmentID()] = segment
			}
			segment.StartPosition = pos.GetStartPosition()
		}

		// assign segments to request
		segments := make([]*datapb.DropVirtualChannelSegment, 0, len(segmentPack))
		for _, segment := range segmentPack {
			segments = append(segments, segment)
		}
		req.Segments = segments

		err := retry.Do(context.Background(), func() error {
			resp, err := dsService.broker.DropVirtualChannel(context.Background(), req)
			if err != nil ||
				resp.GetStatus().GetErrorCode() != commonpb.ErrorCode_Success {
				// meta error, datanode handles a virtual channel does not belong here
				if errors.Is(err, merr.ErrChannelNotFound) ||
					resp.GetStatus().GetErrorCode() == commonpb.ErrorCode_MetaFailed {
					log.Warn("meta error found, skip sync and start to drop virtual channel", zap.String("channel", dsService.vchannelName))
					return nil
				}
				return err
			}
			dsService.channel.transferNewSegments(lo.Map(startPos, func(pos *datapb.SegmentStartPosition, _ int) UniqueID {
				return pos.GetSegmentID()
			}))
			return nil
		}, opts...)
		if err != nil {
			log.Warn("failed to DropVirtualChannel", zap.String("channel", dsService.vchannelName), zap.Error(err))
			panic(err)
		}
		for segID := range segmentPack {
			dsService.channel.segmentFlushed(segID)
			dsService.flushingSegCache.Remove(segID)
		}
	}
}

func flushNotifyFunc(dsService *dataSyncService, opts ...retry.Option) notifyMetaFunc {
	return func(pack *segmentFlushPack) {
		log := log.Ctx(context.Background()).With(
			zap.Int64("segmentID", pack.segmentID),
			zap.Int64("collectionID", dsService.collectionID),
			zap.String("vchannel", dsService.vchannelName),
		)
		if pack.err != nil {
			log.Error("flush pack with error, DataNode quit now", zap.Error(pack.err))
			// TODO silverxia change to graceful stop datanode
			panic(pack.err)
		}

		var (
			fieldInsert = []*datapb.FieldBinlog{}
			fieldStats  = []*datapb.FieldBinlog{}
			deltaInfos  = make([]*datapb.FieldBinlog, 1)
			checkPoints = []*datapb.CheckPoint{}
		)

		for k, v := range pack.insertLogs {
			fieldInsert = append(fieldInsert, &datapb.FieldBinlog{FieldID: k, Binlogs: []*datapb.Binlog{v}})
		}
		for k, v := range pack.statsLogs {
			fieldStats = append(fieldStats, &datapb.FieldBinlog{FieldID: k, Binlogs: []*datapb.Binlog{v}})
		}
		deltaInfos[0] = &datapb.FieldBinlog{Binlogs: pack.deltaLogs}

		// only current segment checkpoint info,
		updates, _ := dsService.channel.getSegmentStatisticsUpdates(pack.segmentID)
		checkPoints = append(checkPoints, &datapb.CheckPoint{
			SegmentID: pack.segmentID,
			// this shouldn't be used because we are not sure this is aligned
			NumOfRows: updates.GetNumRows(),
			Position:  pack.pos,
		})

		startPos := dsService.channel.listNewSegmentsStartPositions()

		log.Info("SaveBinlogPath",
			zap.Int64("SegmentID", pack.segmentID),
			zap.Any("startPos", startPos),
			zap.Any("checkPoints", checkPoints),
			zap.Int("Length of Field2BinlogPaths", len(fieldInsert)),
			zap.Int("Length of Field2Stats", len(fieldStats)),
			zap.Int("Length of Field2Deltalogs", len(deltaInfos[0].GetBinlogs())),
		)

		req := &datapb.SaveBinlogPathsRequest{
			Base: commonpbutil.NewMsgBase(
				commonpbutil.WithMsgType(0),
				commonpbutil.WithMsgID(0),
				commonpbutil.WithSourceID(paramtable.GetNodeID()),
			),
			SegmentID:           pack.segmentID,
			CollectionID:        dsService.collectionID,
			Field2BinlogPaths:   fieldInsert,
			Field2StatslogPaths: fieldStats,
			Deltalogs:           deltaInfos,

			CheckPoints: checkPoints,

			StartPositions: startPos,
			Flushed:        pack.flushed,
			Dropped:        pack.dropped,
			Channel:        dsService.vchannelName,
		}
		err := retry.Do(context.Background(), func() error {
			err := dsService.broker.SaveBinlogPaths(context.Background(), req)
			// Segment not found during stale segment flush. Segment might get compacted already.
			// Stop retry and still proceed to the end, ignoring this error.
			if !pack.flushed && errors.Is(err, merr.ErrSegmentNotFound) {
				log.Warn("stale segment not found, could be compacted")
				log.Warn("failed to SaveBinlogPaths",
					zap.Error(err))
				return nil
			}
			// meta error, datanode handles a virtual channel does not belong here
			if errors.IsAny(err, merr.ErrSegmentNotFound, merr.ErrChannelNotFound) {
				log.Warn("meta error found, skip sync and start to drop virtual channel")
				return nil
			}

			if err != nil {
				return err
			}

			dsService.channel.transferNewSegments(lo.Map(startPos, func(pos *datapb.SegmentStartPosition, _ int) UniqueID {
				return pos.GetSegmentID()
			}))
			return nil
		}, opts...)
		if err != nil {
			log.Warn("failed to SaveBinlogPaths",
				zap.Error(err))
			// TODO change to graceful stop
			panic(err)
		}
		if pack.dropped {
			dsService.channel.removeSegments(pack.segmentID)
		} else if pack.flushed {
			dsService.channel.segmentFlushed(pack.segmentID)
		}

		if dsService.flushListener != nil {
			dsService.flushListener <- pack
		}
		dsService.flushingSegCache.Remove(req.GetSegmentID())
		dsService.channel.evictHistoryInsertBuffer(req.GetSegmentID(), pack.pos)
		dsService.channel.evictHistoryDeleteBuffer(req.GetSegmentID(), pack.pos)
		segment := dsService.channel.getSegment(req.GetSegmentID())
		dsService.channel.updateSingleSegmentMemorySize(req.GetSegmentID())
		segment.setSyncing(false)

		log.Info("successfully save binlog")
	}
}
