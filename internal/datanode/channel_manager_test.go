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
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/milvus-io/milvus-proto/go-api/v2/schemapb"
	"github.com/milvus-io/milvus/internal/proto/datapb"
	"github.com/milvus-io/milvus/pkg/util/paramtable"
)

func TestChannelManagerSuite(t *testing.T) {
	suite.Run(t, new(ChannelManagerSuite))
}

type ChannelManagerSuite struct {
	suite.Suite

	node    *DataNode
	manager *ChannelManager
}

func (s *ChannelManagerSuite) SetupTest() {
	ctx := context.Background()
	s.node = newIDLEDataNodeMock(ctx, schemapb.DataType_Int64)
	s.manager = NewChannelManager(s.node)
}

func getWatchInfoByOpID(opID UniqueID, channel string, state datapb.ChannelWatchState) *datapb.ChannelWatchInfo {
	return &datapb.ChannelWatchInfo{
		OpID:  opID,
		State: state,
		Vchan: &datapb.VchannelInfo{
			CollectionID: 1,
			ChannelName:  channel,
		},
	}
}

func (s *ChannelManagerSuite) TearDownTest() {
	s.manager.Close()
}

func (s *ChannelManagerSuite) TestWatchFail() {
	channel := "by-dev-rootcoord-dml-2"
	paramtable.Get().Save(Params.DataCoordCfg.WatchTimeoutInterval.Key, "0.000001")
	defer paramtable.Get().Reset(Params.DataCoordCfg.WatchTimeoutInterval.Key)
	info := getWatchInfoByOpID(100, channel, datapb.ChannelWatchState_ToWatch)
	s.Require().Equal(0, s.manager.opRunners.Len())
	err := s.manager.Submit(info)
	s.Require().NoError(err)

	opState := <-s.manager.communicateCh
	s.Require().NotNil(opState)
	s.Equal(info.GetOpID(), opState.opID)
	s.Equal(datapb.ChannelWatchState_WatchFailure, opState.state)

	s.manager.handleOpState(opState)

	resp := s.manager.GetProgress(info)
	s.Equal(datapb.ChannelWatchState_WatchFailure, resp.GetState())
}

func (s *ChannelManagerSuite) TestReleaseStuck() {
	var (
		channel  = "by-dev-rootcoord-dml-2"
		stuckSig = make(chan struct{})
	)
	s.manager.releaseFunc = func(channel string) {
		stuckSig <- struct{}{}
	}

	info := getWatchInfoByOpID(100, channel, datapb.ChannelWatchState_ToWatch)
	s.Require().Equal(0, s.manager.opRunners.Len())
	err := s.manager.Submit(info)
	s.Require().NoError(err)

	opState := <-s.manager.communicateCh
	s.Require().NotNil(opState)

	s.manager.handleOpState(opState)

	releaseInfo := getWatchInfoByOpID(101, channel, datapb.ChannelWatchState_ToRelease)
	paramtable.Get().Save(Params.DataCoordCfg.WatchTimeoutInterval.Key, "0.1")
	defer paramtable.Get().Reset(Params.DataCoordCfg.WatchTimeoutInterval.Key)

	err = s.manager.Submit(releaseInfo)
	s.NoError(err)

	opState = <-s.manager.communicateCh
	s.Require().NotNil(opState)
	s.Equal(datapb.ChannelWatchState_ReleaseFailure, opState.state)
	s.manager.handleOpState(opState)

	s.Equal(1, s.manager.abnormals.Len())
	abchannel, ok := s.manager.abnormals.Get(releaseInfo.GetOpID())
	s.True(ok)
	s.Equal(channel, abchannel)

	<-stuckSig

	resp := s.manager.GetProgress(releaseInfo)
	s.Equal(datapb.ChannelWatchState_ReleaseFailure, resp.GetState())
}

func (s *ChannelManagerSuite) TestSubmitIdempotent() {
	channel := "by-dev-rootcoord-dml-1"

	info := getWatchInfoByOpID(100, channel, datapb.ChannelWatchState_ToWatch)
	s.Require().Equal(0, s.manager.opRunners.Len())

	for i := 0; i < 10; i++ {
		err := s.manager.Submit(info)
		s.NoError(err)
	}

	s.Equal(1, s.manager.opRunners.Len())
	s.True(s.manager.opRunners.Contain(channel))

	runner, ok := s.manager.opRunners.Get(channel)
	s.True(ok)
	s.Equal(1, runner.UnfinishedOpSize())
}

func (s *ChannelManagerSuite) TestSubmitWatchAndRelease() {
	channel := "by-dev-rootcoord-dml-0"

	info := getWatchInfoByOpID(100, channel, datapb.ChannelWatchState_ToWatch)

	err := s.manager.Submit(info)
	s.NoError(err)

	opState := <-s.manager.communicateCh
	s.NotNil(opState)
	s.Equal(datapb.ChannelWatchState_WatchSuccess, opState.state)
	s.NotNil(opState.fg)
	s.Equal(info.GetOpID(), opState.fg.opID)

	resp := s.manager.GetProgress(info)
	s.Equal(info.GetOpID(), resp.GetOpID())
	s.Equal(datapb.ChannelWatchState_ToWatch, resp.GetState())

	s.manager.handleOpState(opState)
	s.Equal(1, s.manager.runningFlowgraphs.getFlowGraphNum())
	s.True(s.manager.opRunners.Contain(info.GetVchan().GetChannelName()))
	s.Equal(1, s.manager.opRunners.Len())

	resp = s.manager.GetProgress(info)
	s.Equal(info.GetOpID(), resp.GetOpID())
	s.Equal(datapb.ChannelWatchState_WatchSuccess, resp.GetState())

	// release
	info = getWatchInfoByOpID(101, channel, datapb.ChannelWatchState_ToRelease)

	err = s.manager.Submit(info)
	s.NoError(err)

	opState = <-s.manager.communicateCh
	s.NotNil(opState)
	s.Equal(datapb.ChannelWatchState_ReleaseSuccess, opState.state)
	s.manager.handleOpState(opState)

	resp = s.manager.GetProgress(info)
	s.Equal(info.GetOpID(), resp.GetOpID())
	s.Equal(datapb.ChannelWatchState_ReleaseSuccess, resp.GetState())

	s.Equal(0, s.manager.runningFlowgraphs.getFlowGraphNum())
	s.False(s.manager.opRunners.Contain(info.GetVchan().GetChannelName()))
	s.Equal(0, s.manager.opRunners.Len())
}
