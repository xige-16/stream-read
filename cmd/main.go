package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/xige-16/storage-test/pkg/log"
	"go.uber.org/zap"

	"github.com/xige-16/storage-test/cmd/minio"
	"github.com/xige-16/storage-test/internal/storage"
	"github.com/xige-16/storage-test/pkg/util/paramtable"
)

//type ObjectStorage interface {
//	GetObject(ctx context.Context, bucketName, objectName string, offset int64, size int64) (FileReader, error)
//	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64) error
//	StatObject(ctx context.Context, bucketName, objectName string) (int64, error)
//	ListObjects(ctx context.Context, bucketName string, prefix string, recursive bool) ([]string, []time.Time, error)
//	RemoveObject(ctx context.Context, bucketName, objectName string) error
//}

func main() {
	rps := flag.Int64("rps", 1.0, "rps value")
	size := flag.Int64("size", 1024, "size of data written to minio")
	reqTime := flag.Int64("request_time", 60, "Time for server send requests in seconds")

	// 解析命令行参数
	flag.Parse()
	log.Info("parse args done", zap.Int64("rps", *rps), zap.Int64("data size", *size), zap.Int64("timeout", *reqTime))

	ctx := context.Background()
	paramtable.Init()
	Params := paramtable.Get()
	factory := storage.NewChunkManagerFactoryWithParam(Params)
	chunkManager, err := factory.NewPersistentStorageChunkManager(ctx)
	if err != nil {
		panic("init chunk manager failed!")
	}

	remoteChunkManager := chunkManager.(*storage.RemoteChunkManager)
	minioClient := remoteChunkManager.Client
	bucketName := remoteChunkManager.BucketName
	rootPath := remoteChunkManager.RootPath()

	err = minio.PutObject(ctx, minioClient, bucketName, rootPath, *rps, *size, *reqTime)
	if err != nil {
		log.Error("PutObject error", zap.Error(err))
		return
	}

	err = minio.GetObject(ctx, minioClient, bucketName, rootPath, *rps, *reqTime)
	if err != nil {
		log.Error("GetObject error", zap.Error(err))
		return
	}

	err = minio.RemoveObject(ctx, minioClient, bucketName, rootPath, *rps, *reqTime)
	if err != nil {
		log.Error("RemoveObject error", zap.Error(err))
		return
	}

	fmt.Print("minio func test done!")
}
