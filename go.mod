module github.com/xige-16/storage-test

go 1.20

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.7.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.3.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.1.0
	github.com/aliyun/credentials-go v1.2.7
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cockroachdb/errors v1.9.1
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/klauspost/compress v1.16.5 // indirect
	github.com/milvus-io/milvus-proto/go-api/v2 v2.3.16 // indirect
	github.com/minio/minio-go/v7 v7.0.56
	github.com/prometheus/client_golang v1.14.0 // indirect
	github.com/prometheus/client_model v0.3.0 // indirect
	github.com/prometheus/common v0.42.0 // indirect
	github.com/samber/lo v1.27.0 // indirect
	github.com/spf13/cast v1.3.1 // indirect
	github.com/spf13/viper v1.8.1 // indirect
	github.com/stretchr/testify v1.8.4
	go.opentelemetry.io/otel v1.13.0 // indirect
	go.opentelemetry.io/otel/trace v1.13.0 // indirect
	go.uber.org/atomic v1.10.0
	go.uber.org/multierr v1.7.0 // indirect
	go.uber.org/zap v1.20.0
	golang.org/x/crypto v0.14.0 // indirect
	golang.org/x/exp v0.0.0-20230224173230-c95f2b4c22f2
	golang.org/x/oauth2 v0.7.0
	golang.org/x/sync v0.3.0
	golang.org/x/text v0.13.0 // indirect
	google.golang.org/grpc v1.57.1 // indirect
)

require (
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common v1.0.865
	github.com/xige-16/storage-test/pkg v0.0.0-00010101000000-000000000000
)

require (
	cloud.google.com/go/compute v1.19.1 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.0.0 // indirect
	github.com/alibabacloud-go/debug v0.0.0-20190504072949-9472017b5c68 // indirect
	github.com/alibabacloud-go/tea v1.1.8 // indirect
	github.com/benbjohnson/clock v1.1.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/cockroachdb/logtags v0.0.0-20211118104740-dabe8e521a4f // indirect
	github.com/cockroachdb/redact v1.1.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fsnotify/fsnotify v1.4.9 // indirect
	github.com/getsentry/sentry-go v0.12.0 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.2.4 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/magiconair/properties v1.8.5 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/mitchellh/mapstructure v1.4.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml v1.9.3 // indirect
	github.com/pkg/browser v0.0.0-20210911075715-681adbf594b8 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/procfs v0.9.0 // indirect
	github.com/rogpeppe/go-internal v1.10.0 // indirect
	github.com/rs/xid v1.5.0 // indirect
	github.com/sirupsen/logrus v1.9.2 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/afero v1.6.0 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/objx v0.5.0 // indirect
	github.com/subosito/gotenv v1.2.0 // indirect
	github.com/uber/jaeger-client-go v2.30.0+incompatible // indirect
	go.uber.org/goleak v1.2.0 // indirect
	golang.org/x/net v0.17.0 // indirect
	golang.org/x/sys v0.13.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230525234030-28d5490b6b19 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/apache/pulsar-client-go => github.com/milvus-io/pulsar-client-go v0.6.10
	github.com/expr-lang/expr => github.com/SimFG/expr v0.0.0-20231218130003-94d085776dc5
	github.com/go-kit/kit => github.com/go-kit/kit v0.1.0
	github.com/streamnative/pulsarctl => github.com/xiaofan-luan/pulsarctl v0.5.1
	github.com/tecbot/gorocksdb => github.com/milvus-io/gorocksdb v0.0.0-20220624081344-8c5f4212846b // indirect
	github.com/xige-16/storage-test/pkg => ./pkg
)

exclude github.com/apache/pulsar-client-go/oauth2 v0.0.0-20211108044248-fe3b7c4e445b
