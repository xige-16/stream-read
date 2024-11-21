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

package storage

import (
	"bytes"
	"context"
	"io"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/cockroachdb/errors"
	minio "github.com/minio/minio-go/v7"
	"go.uber.org/zap"
	"golang.org/x/exp/mmap"
	"golang.org/x/sync/errgroup"

	"github.com/xige-16/storage-test/pkg/log"
	"github.com/xige-16/storage-test/pkg/metrics"
	"github.com/xige-16/storage-test/pkg/util/merr"
	"github.com/xige-16/storage-test/pkg/util/retry"
	"github.com/xige-16/storage-test/pkg/util/timerecord"
)

const (
	CloudProviderGCP     = "gcp"
	CloudProviderAWS     = "aws"
	CloudProviderAliyun  = "aliyun"
	CloudProviderAzure   = "azure"
	CloudProviderTencent = "tencent"
)

type ObjectStorage interface {
	GetObject(ctx context.Context, bucketName, objectName string, offset int64, size int64) (FileReader, error)
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64) error
	StatObject(ctx context.Context, bucketName, objectName string) (int64, error)
	ListObjects(ctx context.Context, bucketName string, prefix string, recursive bool) ([]string, []time.Time, error)
	RemoveObject(ctx context.Context, bucketName, objectName string) error
}

// RemoteChunkManager is responsible for read and write data stored in minio.
type RemoteChunkManager struct {
	Client ObjectStorage

	//	ctx        context.Context
	BucketName string
	rootPath   string
}

var _ ChunkManager = (*RemoteChunkManager)(nil)

func NewRemoteChunkManager(ctx context.Context, c *config) (*RemoteChunkManager, error) {
	var client ObjectStorage
	var err error
	if c.cloudProvider == CloudProviderAzure {
		client, err = newAzureObjectStorageWithConfig(ctx, c)
	} else {
		client, err = newMinioObjectStorageWithConfig(ctx, c)
	}
	if err != nil {
		return nil, err
	}
	mcm := &RemoteChunkManager{
		Client:     client,
		BucketName: c.bucketName,
		rootPath:   strings.TrimLeft(c.rootPath, "/"),
	}
	log.Info("remote chunk manager init success.", zap.String("remote", c.cloudProvider), zap.String("bucketname", c.bucketName), zap.String("root", mcm.RootPath()))
	return mcm, nil
}

// RootPath returns minio root path.
func (mcm *RemoteChunkManager) RootPath() string {
	return mcm.rootPath
}

// Path returns the path of minio data if exists.
func (mcm *RemoteChunkManager) Path(ctx context.Context, filePath string) (string, error) {
	exist, err := mcm.Exist(ctx, filePath)
	if err != nil {
		return "", err
	}
	if !exist {
		return "", errors.New("minio file manage cannot be found with filePath:" + filePath)
	}
	return filePath, nil
}

// Reader returns the path of minio data if exists.
func (mcm *RemoteChunkManager) Reader(ctx context.Context, filePath string) (FileReader, error) {
	reader, err := mcm.getObject(ctx, mcm.BucketName, filePath, int64(0), int64(0))
	if err != nil {
		log.Warn("failed to get object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
		return nil, err
	}
	return reader, nil
}

func (mcm *RemoteChunkManager) Size(ctx context.Context, filePath string) (int64, error) {
	objectInfo, err := mcm.getObjectSize(ctx, mcm.BucketName, filePath)
	if err != nil {
		log.Warn("failed to stat object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
		return 0, err
	}

	return objectInfo, nil
}

// Write writes the data to minio storage.
func (mcm *RemoteChunkManager) Write(ctx context.Context, filePath string, content []byte) error {
	err := mcm.putObject(ctx, mcm.BucketName, filePath, bytes.NewReader(content), int64(len(content)))
	if err != nil {
		log.Warn("failed to put object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
		return err
	}

	metrics.PersistentDataKvSize.WithLabelValues(metrics.DataPutLabel).Observe(float64(len(content)))
	return nil
}

// MultiWrite saves multiple objects, the path is the key of @kvs.
// The object value is the value of @kvs.
func (mcm *RemoteChunkManager) MultiWrite(ctx context.Context, kvs map[string][]byte) error {
	var el error
	for key, value := range kvs {
		err := mcm.Write(ctx, key, value)
		if err != nil {
			el = merr.Combine(el, errors.Wrapf(err, "failed to write %s", key))
		}
	}
	return el
}

// Exist checks whether chunk is saved to minio storage.
func (mcm *RemoteChunkManager) Exist(ctx context.Context, filePath string) (bool, error) {
	_, err := mcm.getObjectSize(ctx, mcm.BucketName, filePath)
	if err != nil {
		if IsErrNoSuchKey(err) {
			return false, nil
		}
		log.Warn("failed to stat object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
		return false, err
	}
	return true, nil
}

// Read reads the minio storage data if exists.
func (mcm *RemoteChunkManager) Read(ctx context.Context, filePath string) ([]byte, error) {
	var data []byte
	err := retry.Do(ctx, func() error {
		object, err := mcm.getObject(ctx, mcm.BucketName, filePath, int64(0), int64(0))
		if err != nil {
			log.Warn("failed to get object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
			return err
		}
		defer object.Close()

		// Prefetch object data
		var empty []byte
		_, err = object.Read(empty)
		if err != nil {
			switch err := err.(type) {
			case *azcore.ResponseError:
				if err.ErrorCode == string(bloberror.BlobNotFound) {
					return WrapErrNoSuchKey(filePath)
				}
			case minio.ErrorResponse:
				if err.Code == "NoSuchKey" {
					return WrapErrNoSuchKey(filePath)
				}
			}
			log.Warn("failed to read object", zap.String("path", filePath), zap.Error(err))
			return checkObjectStorageError(filePath, err)
		}
		size, err := mcm.getObjectSize(ctx, mcm.BucketName, filePath)
		if err != nil {
			log.Warn("failed to stat object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
			return err
		}
		data, err = Read(object, size)
		if err != nil {
			if err == io.ErrUnexpectedEOF {
				return merr.WrapErrIoUnexpectEOF(filePath, err)
			}
			errResponse := minio.ToErrorResponse(err)
			if errResponse.Code == "NoSuchKey" {
				return WrapErrNoSuchKey(filePath)
			}
			log.Warn("failed to read object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
			return err
		}
		metrics.PersistentDataKvSize.WithLabelValues(metrics.DataGetLabel).Observe(float64(size))
		return nil
	}, retry.Attempts(3), retry.RetryErr(merr.IsRetryableErr))
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (mcm *RemoteChunkManager) MultiRead(ctx context.Context, keys []string) ([][]byte, error) {
	var el error
	var objectsValues [][]byte
	for _, key := range keys {
		objectValue, err := mcm.Read(ctx, key)
		if err != nil {
			el = merr.Combine(el, errors.Wrapf(err, "failed to read %s", key))
		}
		objectsValues = append(objectsValues, objectValue)
	}

	return objectsValues, el
}

func (mcm *RemoteChunkManager) ReadWithPrefix(ctx context.Context, prefix string) ([]string, [][]byte, error) {
	objectsKeys, _, err := mcm.ListWithPrefix(ctx, prefix, true)
	if err != nil {
		return nil, nil, err
	}
	objectsValues, err := mcm.MultiRead(ctx, objectsKeys)
	if err != nil {
		return nil, nil, err
	}

	return objectsKeys, objectsValues, nil
}

func (mcm *RemoteChunkManager) Mmap(ctx context.Context, filePath string) (*mmap.ReaderAt, error) {
	return nil, errors.New("this method has not been implemented")
}

// ReadAt reads specific position data of minio storage if exists.
func (mcm *RemoteChunkManager) ReadAt(ctx context.Context, filePath string, off int64, length int64) ([]byte, error) {
	if off < 0 || length < 0 {
		return nil, io.EOF
	}

	object, err := mcm.getObject(ctx, mcm.BucketName, filePath, off, length)
	if err != nil {
		log.Warn("failed to get object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
		return nil, err
	}
	defer object.Close()

	data, err := Read(object, length)
	if err != nil {
		switch err := err.(type) {
		case *azcore.ResponseError:
			if err.ErrorCode == string(bloberror.BlobNotFound) {
				return nil, WrapErrNoSuchKey(filePath)
			}
		case minio.ErrorResponse:
			if err.Code == "NoSuchKey" {
				return nil, WrapErrNoSuchKey(filePath)
			}
		}
		log.Warn("failed to read object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
		return nil, err
	}
	metrics.PersistentDataKvSize.WithLabelValues(metrics.DataGetLabel).Observe(float64(length))
	return data, nil
}

// Remove deletes an object with @key.
func (mcm *RemoteChunkManager) Remove(ctx context.Context, filePath string) error {
	err := mcm.removeObject(ctx, mcm.BucketName, filePath)
	if err != nil {
		log.Warn("failed to remove object", zap.String("bucket", mcm.BucketName), zap.String("path", filePath), zap.Error(err))
		return err
	}
	return nil
}

// MultiRemove deletes a objects with @keys.
func (mcm *RemoteChunkManager) MultiRemove(ctx context.Context, keys []string) error {
	var el error
	for _, key := range keys {
		err := mcm.Remove(ctx, key)
		if err != nil {
			el = merr.Combine(el, errors.Wrapf(err, "failed to remove %s", key))
		}
	}
	return el
}

// RemoveWithPrefix removes all objects with the same prefix @prefix from minio.
func (mcm *RemoteChunkManager) RemoveWithPrefix(ctx context.Context, prefix string) error {
	removeKeys, _, err := mcm.listObjects(ctx, mcm.BucketName, prefix, true)
	if err != nil {
		return err
	}
	i := 0
	maxGoroutine := 10
	for i < len(removeKeys) {
		runningGroup, groupCtx := errgroup.WithContext(ctx)
		for j := 0; j < maxGoroutine && i < len(removeKeys); j++ {
			key := removeKeys[i]
			runningGroup.Go(func() error {
				err := mcm.removeObject(groupCtx, mcm.BucketName, key)
				if err != nil {
					log.Warn("failed to remove object", zap.String("path", key), zap.Error(err))
					return err
				}
				return nil
			})
			i++
		}
		if err := runningGroup.Wait(); err != nil {
			return err
		}
	}
	return nil
}

// ListWithPrefix returns objects with provided prefix.
// by default, if `recursive`=false, list object with return object with path under save level
// say minio has followinng objects: [a, ab, a/b, ab/c]
// calling `ListWithPrefix` with `prefix` = a && `recursive` = false will only returns [a, ab]
// If caller needs all objects without level limitation, `recursive` shall be true.
func (mcm *RemoteChunkManager) ListWithPrefix(ctx context.Context, prefix string, recursive bool) ([]string, []time.Time, error) {
	// cannot use ListObjects(ctx, BucketName, Opt{Prefix:prefix, Recursive:true})
	// if minio has lots of objects under the provided path
	// recursive = true may timeout during the recursive browsing the objects.
	// See also: https://github.com/milvus-io/milvus/issues/19095

	// TODO add concurrent call if performance matters
	// only return current level per call
	return mcm.listObjects(ctx, mcm.BucketName, prefix, recursive)
}

func (mcm *RemoteChunkManager) getObject(ctx context.Context, bucketName, objectName string,
	offset int64, size int64,
) (FileReader, error) {
	reader, err := mcm.Client.GetObject(ctx, bucketName, objectName, offset, size)
	metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataGetLabel, metrics.TotalLabel).Inc()
	if err == nil && reader != nil {
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataGetLabel, metrics.SuccessLabel).Inc()
	} else {
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataGetLabel, metrics.FailLabel).Inc()
	}

	switch err := err.(type) {
	case *azcore.ResponseError:
		if err.ErrorCode == string(bloberror.BlobNotFound) {
			return nil, WrapErrNoSuchKey(objectName)
		}
	case minio.ErrorResponse:
		if err.Code == "NoSuchKey" {
			return nil, WrapErrNoSuchKey(objectName)
		}
	}

	return reader, err
}

func (mcm *RemoteChunkManager) putObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64) error {
	start := timerecord.NewTimeRecorder("putObject")

	err := mcm.Client.PutObject(ctx, bucketName, objectName, reader, objectSize)
	metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataPutLabel, metrics.TotalLabel).Inc()
	if err == nil {
		metrics.PersistentDataRequestLatency.WithLabelValues(metrics.DataPutLabel).Observe(float64(start.ElapseSpan().Milliseconds()))
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.MetaPutLabel, metrics.SuccessLabel).Inc()
	} else {
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.MetaPutLabel, metrics.FailLabel).Inc()
	}

	return err
}

func (mcm *RemoteChunkManager) getObjectSize(ctx context.Context, bucketName, objectName string) (int64, error) {
	start := timerecord.NewTimeRecorder("getObjectSize")

	info, err := mcm.Client.StatObject(ctx, bucketName, objectName)
	metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataStatLabel, metrics.TotalLabel).Inc()
	if err == nil {
		metrics.PersistentDataRequestLatency.WithLabelValues(metrics.DataStatLabel).Observe(float64(start.ElapseSpan().Milliseconds()))
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataStatLabel, metrics.SuccessLabel).Inc()
	} else {
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataStatLabel, metrics.FailLabel).Inc()
	}

	switch err := err.(type) {
	case *azcore.ResponseError:
		if err.ErrorCode == string(bloberror.BlobNotFound) {
			return info, WrapErrNoSuchKey(objectName)
		}
	case minio.ErrorResponse:
		if err.Code == "NoSuchKey" {
			return info, WrapErrNoSuchKey(objectName)
		}
	}

	return info, err
}

func (mcm *RemoteChunkManager) listObjects(ctx context.Context, bucketName string, prefix string, recursive bool) ([]string, []time.Time, error) {
	start := timerecord.NewTimeRecorder("listObjects")

	blobNames, lastModifiedTime, err := mcm.Client.ListObjects(ctx, bucketName, prefix, recursive)
	metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataListLabel, metrics.TotalLabel).Inc()
	if err == nil {
		metrics.PersistentDataRequestLatency.WithLabelValues(metrics.DataListLabel).Observe(float64(start.ElapseSpan().Milliseconds()))
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataListLabel, metrics.SuccessLabel).Inc()
	} else {
		log.Warn("failed to list with prefix", zap.String("bucket", mcm.BucketName), zap.String("prefix", prefix), zap.Error(err))
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataListLabel, metrics.FailLabel).Inc()
	}
	return blobNames, lastModifiedTime, err
}

func (mcm *RemoteChunkManager) removeObject(ctx context.Context, bucketName, objectName string) error {
	start := timerecord.NewTimeRecorder("removeObject")

	err := mcm.Client.RemoveObject(ctx, bucketName, objectName)
	metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataRemoveLabel, metrics.TotalLabel).Inc()
	if err == nil {
		metrics.PersistentDataRequestLatency.WithLabelValues(metrics.DataRemoveLabel).Observe(float64(start.ElapseSpan().Milliseconds()))
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataRemoveLabel, metrics.SuccessLabel).Inc()
	} else {
		metrics.PersistentDataOpCounter.WithLabelValues(metrics.DataRemoveLabel, metrics.FailLabel).Inc()
	}

	return err
}

func checkObjectStorageError(fileName string, err error) error {
	if err == nil {
		return nil
	}

	switch err := err.(type) {
	case *azcore.ResponseError:
		if err.ErrorCode == string(bloberror.BlobNotFound) {
			return merr.WrapErrIoKeyNotFound(fileName, err.Error())
		}
		return merr.WrapErrIoFailed(fileName, err)
	case minio.ErrorResponse:
		if err.Code == "NoSuchKey" {
			return merr.WrapErrIoKeyNotFound(fileName, err.Error())
		}
		return merr.WrapErrIoFailed(fileName, err)
	}
	if err == io.ErrUnexpectedEOF {
		return merr.WrapErrIoUnexpectEOF(fileName, err)
	}
	return merr.WrapErrIoFailed(fileName, err)
}
