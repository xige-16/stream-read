package main

import (
	"context"
	"encoding/base64"
	"flag"
	"github.com/xige-16/stream-read/pkg/util/funcutil"
	"time"

	"github.com/golang/protobuf/proto"
	"go.uber.org/zap"

	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/msgpb"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"github.com/xige-16/stream-read/pkg/log"
	"github.com/xige-16/stream-read/pkg/mq/msgstream"
	"github.com/xige-16/stream-read/pkg/mq/msgstream/mqwrapper"
	"github.com/xige-16/stream-read/pkg/util/paramtable"
	"github.com/xige-16/stream-read/pkg/util/tsoutil"
)

func main() {
	dbName := flag.String("db_name", "", "database name")
	collectionName := flag.String("collection_name", "", "collection name")
	collectionID := flag.Int64("collection_id", 0, "collection id")
	topic := flag.String("topic_name", "", "topic name")
	pos := flag.String("sub_pos", "", "sub pos")
	subName := flag.String("sub_name", "recovery-milvus", "sub name")

	milvusAddress := flag.String("milvus_address", "", "milvus address")
	milvusUser := flag.String("milvus_user", "", "milvus user")
	milvusPass := flag.String("milvus_password", "", "milvus password")

	autoIDFieldName := flag.String("auto_id_field_name", "", "auto id field name")

	// 解析命令行参数
	flag.Parse()
	log.Info("parse args done", zap.String("dbName", *dbName),
		zap.String("collectionName", *collectionName),
		zap.Int64("collectionID", *collectionID),
		zap.String("topic", *topic),
		zap.String("pos", *pos),
		zap.String("subName", *subName),
		zap.String("milvus address", *milvusAddress),
		zap.String("milvus user", *milvusUser),
		zap.String("milvus pass", *milvusPass),
		zap.String("auto id field name", *autoIDFieldName))

	startTime := time.Now().Unix()

	positionByte, err := base64.StdEncoding.DecodeString(*pos)
	if err != nil {
		panic("decode pos failed!, " + err.Error())
	}
	position := &msgpb.MsgPosition{}
	err = proto.Unmarshal(positionByte, position)
	if err != nil {
		panic("unmarshal position failed!, " + err.Error())
	}
	pChan := funcutil.ToPhysicalChannel(position.ChannelName)
	if pChan != *topic {
		panic("topic not consistent with pos, expect = " + *topic + ", actual = " + pChan)
	}
	position.ChannelName = pChan

	ctx := context.Background()
	paramtable.Init()
	Params := paramtable.Get()
	factory := msgstream.NewPmsFactory(&Params.ServiceParam)
	stream, err := factory.NewTtMsgStream(ctx)
	if err != nil {
		panic("init msg stream failed!, " + err.Error())
	}

	log := log.With(zap.String("topic", *topic), zap.String("subName", *subName))
	log.Info("creating consumer...")
	if len(*pos) == 0 {
		panic("empty pos!")
	}
	err = stream.AsConsumer(ctx, []string{*topic}, *subName, mqwrapper.SubscriptionPositionUnknown)
	if err != nil {
		panic("asConsumer failed!, " + err.Error())
	}

	log.Info("start seek", zap.Any("pos", position))
	err = stream.Seek(ctx, []*msgpb.MsgPosition{position})
	if err != nil {
		stream.Close()
		panic("seek failed!, " + err.Error())
	}
	log.Info("seek done!")

	client, err := client.NewClient(ctx, client.Config{
		Address:  *milvusAddress,
		Username: *milvusUser,
		Password: *milvusPass,
		DBName:   *dbName,
	})
	if err != nil {
		panic("init milvus go client failed, " + err.Error())
	}
	defer client.Close()

	log.Info("init milvus client done!")
	for {
		select {
		case <-ctx.Done():
			stream.Close()
			return
		case msgs, ok := <-stream.Chan():
			if !ok {
				return
			}
			timeOfBegin, _ := tsoutil.ParseTS(msgs.BeginTs)
			log.Info("update recover process", zap.Int64("start time", startTime), zap.Int64("msg time", timeOfBegin.Unix()))
			if timeOfBegin.Unix() >= startTime {
				log.Info("recover done!")
				return
			}
			for _, msg := range msgs.Msgs {
				switch msg.Type() {
				case commonpb.MsgType_Insert:
					imsg := msg.(*msgstream.InsertMsg)
					imsgColname := imsg.GetCollectionName()
					imsgCollID := imsg.GetCollectionID()
					imsgPartName := imsg.GetPartitionName()
					imsgFieldDatas := imsg.GetFieldsData()
					numRows := imsg.GetNumRows()

					if *collectionID != imsgCollID || *collectionName != imsgColname {
						continue
					}

					log.Info("receive insert messages",
						zap.String("coll", imsgColname),
						zap.String("part", imsgPartName),
						zap.Uint64("numRows", numRows))

					columes := make([]entity.Column, 0)
					var convertErr error
					for _, fd := range imsgFieldDatas {
						if len(*autoIDFieldName) != 0 && *autoIDFieldName != fd.GetFieldName() {
							colume, err := entity.FieldDataColumn(fd, 0, int(numRows))

							if err != nil {
								convertErr = err
								break
							}
							columes = append(columes, colume)
						}
					}
					if convertErr != nil {
						log.Error("convert inert msg failed", zap.Error(convertErr))
						continue
					}

					_, err = client.Insert(ctx, imsgColname, imsgPartName, columes...)
					if err != nil {
						log.Error("insert msg failed", zap.Error(err))
					}

				case commonpb.MsgType_Delete:
					dmsg := msg.(*msgstream.DeleteMsg)
					dmsgColname := dmsg.GetCollectionName()
					dmsgColID := dmsg.GetCollectionID()
					dmsgPartName := dmsg.GetPartitionName()
					dmsgIDs := dmsg.GetPrimaryKeys()
					numRows := dmsg.GetNumRows()

					if *collectionID != dmsgColID || *collectionName != dmsgColname {
						continue
					}

					log.Info("receive delete messages", zap.Int64("numRows", dmsg.NumRows))

					colume, err := entity.IDColumns(dmsgIDs, 0, int(numRows))
					if err != nil {
						log.Error("convert delete pks failed", zap.Error(err))
						continue
					}

					err = client.DeleteByPks(ctx, dmsgColname, dmsgPartName, colume)
					if err != nil {
						log.Error("delete msg failed", zap.Error(err))
					}
				case commonpb.MsgType_DropCollection:
					dropmsg := msg.(*msgstream.DropCollectionMsg)
					if *collectionID == dropmsg.GetCollectionID() {
						log.Info("collection droped, recovery done!")
						return
					}
				}
			}
		}
	}
}
