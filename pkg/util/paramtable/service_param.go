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

package paramtable

import (
	"path"
	"strings"
)

// ServiceParam is used to quickly and easily access all basic service configurations.
type ServiceParam struct {
	LocalStorageCfg LocalStorageConfig
	MinioCfg        MinioConfig
}

func (p *ServiceParam) init(bt *BaseTable) {
	p.LocalStorageCfg.Init(bt)
	p.MinioCfg.Init(bt)
}

type LocalStorageConfig struct {
	Path ParamItem `refreshable:"false"`
}

func (p *LocalStorageConfig) Init(base *BaseTable) {
	p.Path = ParamItem{
		Key:          "localStorage.path",
		Version:      "2.0.0",
		DefaultValue: "/var/lib/milvus/data",
		Doc:          "please adjust in embedded Milvus: /tmp/milvus/data/",
		Export:       true,
	}
	p.Path.Init(base.mgr)
}

// /////////////////////////////////////////////////////////////////////////////
// --- minio ---
type MinioConfig struct {
	Address          ParamItem `refreshable:"false"`
	Port             ParamItem `refreshable:"false"`
	AccessKeyID      ParamItem `refreshable:"false"`
	SecretAccessKey  ParamItem `refreshable:"false"`
	UseSSL           ParamItem `refreshable:"false"`
	SslCACert        ParamItem `refreshable:"false"`
	BucketName       ParamItem `refreshable:"false"`
	RootPath         ParamItem `refreshable:"false"`
	UseIAM           ParamItem `refreshable:"false"`
	CloudProvider    ParamItem `refreshable:"false"`
	IAMEndpoint      ParamItem `refreshable:"false"`
	LogLevel         ParamItem `refreshable:"false"`
	Region           ParamItem `refreshable:"false"`
	UseVirtualHost   ParamItem `refreshable:"false"`
	RequestTimeoutMs ParamItem `refreshable:"false"`
}

func (p *MinioConfig) Init(base *BaseTable) {
	p.Port = ParamItem{
		Key:          "minio.port",
		DefaultValue: "9000",
		Version:      "2.0.0",
		Doc:          "Port of MinIO/S3",
		PanicIfEmpty: true,
		Export:       true,
	}
	p.Port.Init(base.mgr)

	p.Address = ParamItem{
		Key:          "minio.address",
		DefaultValue: "",
		Version:      "2.0.0",
		Formatter: func(addr string) string {
			if addr == "" {
				return ""
			}
			if strings.Contains(addr, ":") {
				return addr
			}
			port, _ := p.Port.get()
			return addr + ":" + port
		},
		Doc:    "Address of MinIO/S3",
		Export: true,
	}
	p.Address.Init(base.mgr)

	p.AccessKeyID = ParamItem{
		Key:          "minio.accessKeyID",
		Version:      "2.0.0",
		DefaultValue: "minioadmin",
		PanicIfEmpty: false, // tmp fix, need to be conditional
		Doc:          "accessKeyID of MinIO/S3",
		Export:       true,
	}
	p.AccessKeyID.Init(base.mgr)

	p.SecretAccessKey = ParamItem{
		Key:          "minio.secretAccessKey",
		Version:      "2.0.0",
		DefaultValue: "minioadmin",
		PanicIfEmpty: false, // tmp fix, need to be conditional
		Doc:          "MinIO/S3 encryption string",
		Export:       true,
	}
	p.SecretAccessKey.Init(base.mgr)

	p.UseSSL = ParamItem{
		Key:          "minio.useSSL",
		Version:      "2.0.0",
		DefaultValue: "false",
		PanicIfEmpty: true,
		Doc:          "Access to MinIO/S3 with SSL",
		Export:       true,
	}
	p.UseSSL.Init(base.mgr)

	p.SslCACert = ParamItem{
		Key:     "minio.ssl.tlsCACert",
		Version: "2.3.12",
		Doc:     "path to your CACert file",
		Export:  true,
	}
	p.SslCACert.Init(base.mgr)

	p.BucketName = ParamItem{
		Key:          "minio.bucketName",
		Version:      "2.0.0",
		DefaultValue: "a-bucket",
		PanicIfEmpty: true,
		Doc:          "Bucket name in MinIO/S3",
		Export:       true,
	}
	p.BucketName.Init(base.mgr)

	p.RootPath = ParamItem{
		Key:     "minio.rootPath",
		Version: "2.0.0",
		Formatter: func(rootPath string) string {
			if rootPath == "" {
				return ""
			}
			rootPath = strings.TrimLeft(rootPath, "/")
			return path.Clean(rootPath)
		},
		PanicIfEmpty: false,
		Doc:          "The root path where the message is stored in MinIO/S3",
		Export:       true,
	}
	p.RootPath.Init(base.mgr)

	p.UseIAM = ParamItem{
		Key:          "minio.useIAM",
		DefaultValue: DefaultMinioUseIAM,
		Version:      "2.0.0",
		Doc: `Whether to useIAM role to access S3/GCS instead of access/secret keys
For more information, refer to
aws: https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_use.html
gcp: https://cloud.google.com/storage/docs/access-control/iam
aliyun (ack): https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control
aliyun (ecs): https://www.alibabacloud.com/help/en/elastic-compute-service/latest/attach-an-instance-ram-role`,
		Export: true,
	}
	p.UseIAM.Init(base.mgr)

	p.CloudProvider = ParamItem{
		Key:          "minio.cloudProvider",
		DefaultValue: DefaultMinioCloudProvider,
		Version:      "2.2.0",
		Doc: `Cloud Provider of S3. Supports: "aws", "gcp", "aliyun".
You can use "aws" for other cloud provider supports S3 API with signature v4, e.g.: minio
You can use "gcp" for other cloud provider supports S3 API with signature v2
You can use "aliyun" for other cloud provider uses virtual host style bucket
When useIAM enabled, only "aws", "gcp", "aliyun" is supported for now`,
		Export: true,
	}
	p.CloudProvider.Init(base.mgr)

	p.IAMEndpoint = ParamItem{
		Key:          "minio.iamEndpoint",
		DefaultValue: DefaultMinioIAMEndpoint,
		Version:      "2.0.0",
		Doc: `Custom endpoint for fetch IAM role credentials. when useIAM is true & cloudProvider is "aws".
Leave it empty if you want to use AWS default endpoint`,
		Export: true,
	}
	p.IAMEndpoint.Init(base.mgr)
	p.LogLevel = ParamItem{
		Key:          "minio.logLevel",
		DefaultValue: "fatal",
		Version:      "2.3.0",
		Doc:          `Log level for aws sdk log. Supported level:  off, fatal, error, warn, info, debug, trace`,
		Export:       true,
	}
	p.LogLevel.Init(base.mgr)
	p.Region = ParamItem{
		Key:          "minio.region",
		DefaultValue: DefaultMinioRegion,
		Version:      "2.3.0",
		Doc:          `Specify minio storage system location region`,
		Export:       true,
	}
	p.Region.Init(base.mgr)

	p.UseVirtualHost = ParamItem{
		Key:          "minio.useVirtualHost",
		Version:      "2.3.0",
		DefaultValue: DefaultMinioUseVirtualHost,
		PanicIfEmpty: true,
		Doc:          "Whether use virtual host mode for bucket",
		Export:       true,
	}
	p.UseVirtualHost.Init(base.mgr)

	p.RequestTimeoutMs = ParamItem{
		Key:          "minio.requestTimeoutMs",
		Version:      "2.3.2",
		DefaultValue: DefaultMinioRequestTimeout,
		Doc:          "minio timeout for request time in milliseconds",
		Export:       true,
	}
	p.RequestTimeoutMs.Init(base.mgr)
}
