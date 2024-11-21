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

package paramtable

import (
	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/xige-16/storage-test/pkg/config"
	"sync"
)

const (
	// DefaultIndexSliceSize defines the default slice size of index file when serializing.
	DefaultIndexSliceSize                      = 16
	DefaultConsistencyLevelUsedInDelete        = commonpb.ConsistencyLevel_Bounded
	DefaultGracefulTime                        = 5000 // ms
	DefaultGracefulStopTimeout                 = 1800 // s, for node
	DefaultFlushMgrCleanInterval               = 300  // s
	DefaultProxyGracefulStopTimeout            = 30   // s，for proxy
	DefaultCoordGracefulStopTimeout            = 5    // s，for coord
	DefaultHighPriorityThreadCoreCoefficient   = 10
	DefaultMiddlePriorityThreadCoreCoefficient = 5
	DefaultLowPriorityThreadCoreCoefficient    = 1

	DefaultSessionTTL        = 30 // s
	DefaultSessionRetryTimes = 30

	DefaultMaxDegree                = 56
	DefaultSearchListSize           = 100
	DefaultPQCodeBudgetGBRatio      = 0.125
	DefaultBuildNumThreadsRatio     = 1.0
	DefaultSearchCacheBudgetGBRatio = 0.10
	DefaultLoadNumThreadRatio       = 8.0
	DefaultBeamWidthRatio           = 4.0
)

// ComponentParam is used to quickly and easily access all components' configurations.
type ComponentParam struct {
	ServiceParam
	once      sync.Once
	baseTable *BaseTable
	CommonCfg commonConfig

	HTTPCfg httpConfig
	LogCfg  logConfig
}

// Init initialize once
func (p *ComponentParam) Init(bt *BaseTable) {
	p.once.Do(func() {
		p.init(bt)
	})
}

// init initialize the global param table

func (p *ComponentParam) init(bt *BaseTable) {
	p.baseTable = bt
	p.ServiceParam.init(bt)
	p.CommonCfg.init(bt)

	p.HTTPCfg.init(bt)
	p.LogCfg.init(bt)
}

func (p *ComponentParam) GetComponentConfigurations(componentName string, sub string) map[string]string {
	allownPrefixs := append(globalConfigPrefixs(), componentName+".")
	return p.baseTable.mgr.GetBy(config.WithSubstr(sub), config.WithOneOfPrefixs(allownPrefixs...))
}

func (p *ComponentParam) GetAll() map[string]string {
	return p.baseTable.mgr.GetConfigs()
}

func (p *ComponentParam) Watch(key string, watcher config.EventHandler) {
	p.baseTable.mgr.Dispatcher.Register(key, watcher)
}

func (p *ComponentParam) Unwatch(key string, watcher config.EventHandler) {
	p.baseTable.mgr.Dispatcher.Unregister(key, watcher)
}

func (p *ComponentParam) WatchKeyPrefix(keyPrefix string, watcher config.EventHandler) {
	p.baseTable.mgr.Dispatcher.RegisterForKeyPrefix(keyPrefix, watcher)
}

// /////////////////////////////////////////////////////////////////////////////
// --- common ---
type commonConfig struct {
	StorageType ParamItem `refreshable:"false"`
}

func (p *commonConfig) init(base *BaseTable) {
	p.StorageType = ParamItem{
		Key:          "common.storageType",
		Version:      "2.0.0",
		DefaultValue: "remote",
		Doc:          "please adjust in embedded Milvus: local, available values are [local, remote, opendal], value minio is deprecated, use remote instead",
		Export:       true,
	}
	p.StorageType.Init(base.mgr)
}

type traceConfig struct {
	Exporter       ParamItem `refreshable:"false"`
	SampleFraction ParamItem `refreshable:"false"`
	JaegerURL      ParamItem `refreshable:"false"`
	OtlpEndpoint   ParamItem `refreshable:"false"`
	OtlpSecure     ParamItem `refreshable:"false"`
}

func (t *traceConfig) init(base *BaseTable) {
	t.Exporter = ParamItem{
		Key:     "trace.exporter",
		Version: "2.3.0",
		Doc: `trace exporter type, default is stdout,
optional values: ['stdout', 'jaeger']`,
		Export: true,
	}
	t.Exporter.Init(base.mgr)

	t.SampleFraction = ParamItem{
		Key:          "trace.sampleFraction",
		Version:      "2.3.0",
		DefaultValue: "0",
		Doc: `fraction of traceID based sampler,
optional values: [0, 1]
Fractions >= 1 will always sample. Fractions < 0 are treated as zero.`,
		Export: true,
	}
	t.SampleFraction.Init(base.mgr)

	t.JaegerURL = ParamItem{
		Key:     "trace.jaeger.url",
		Version: "2.3.0",
		Doc:     "when exporter is jaeger should set the jaeger's URL",
		Export:  true,
	}
	t.JaegerURL.Init(base.mgr)

	t.OtlpEndpoint = ParamItem{
		Key:     "trace.otlp.endpoint",
		Version: "2.3.0",
		Doc:     "example: \"127.0.0.1:4318\"",
	}
	t.OtlpEndpoint.Init(base.mgr)

	t.OtlpSecure = ParamItem{
		Key:          "trace.otlp.secure",
		Version:      "2.4.0",
		DefaultValue: "true",
	}
	t.OtlpSecure.Init(base.mgr)
}

type logConfig struct {
	Level        ParamItem `refreshable:"false"`
	RootPath     ParamItem `refreshable:"false"`
	MaxSize      ParamItem `refreshable:"false"`
	MaxAge       ParamItem `refreshable:"false"`
	MaxBackups   ParamItem `refreshable:"false"`
	Format       ParamItem `refreshable:"false"`
	Stdout       ParamItem `refreshable:"false"`
	GrpcLogLevel ParamItem `refreshable:"false"`
}

func (l *logConfig) init(base *BaseTable) {
	l.Level = ParamItem{
		Key:          "log.level",
		DefaultValue: "info",
		Version:      "2.0.0",
		Doc:          "Only supports debug, info, warn, error, panic, or fatal. Default 'info'.",
		Export:       true,
	}
	l.Level.Init(base.mgr)

	l.RootPath = ParamItem{
		Key:     "log.file.rootPath",
		Version: "2.0.0",
		Doc:     "root dir path to put logs, default \"\" means no log file will print. please adjust in embedded Milvus: /tmp/milvus/logs",
		Export:  true,
	}
	l.RootPath.Init(base.mgr)

	l.MaxSize = ParamItem{
		Key:          "log.file.maxSize",
		DefaultValue: "300",
		Version:      "2.0.0",
		Doc:          "MB",
		Export:       true,
	}
	l.MaxSize.Init(base.mgr)

	l.MaxAge = ParamItem{
		Key:          "log.file.maxAge",
		DefaultValue: "10",
		Version:      "2.0.0",
		Doc:          "Maximum time for log retention in day.",
		Export:       true,
	}
	l.MaxAge.Init(base.mgr)

	l.MaxBackups = ParamItem{
		Key:          "log.file.maxBackups",
		DefaultValue: "20",
		Version:      "2.0.0",
		Export:       true,
	}
	l.MaxBackups.Init(base.mgr)

	l.Format = ParamItem{
		Key:          "log.format",
		DefaultValue: "text",
		Version:      "2.0.0",
		Doc:          "text or json",
		Export:       true,
	}
	l.Format.Init(base.mgr)

	l.Stdout = ParamItem{
		Key:          "log.stdout",
		DefaultValue: "true",
		Version:      "2.3.0",
		Doc:          "Stdout enable or not",
		Export:       true,
	}
	l.Stdout.Init(base.mgr)

	l.GrpcLogLevel = ParamItem{
		Key:          "grpc.log.level",
		DefaultValue: "WARNING",
		Version:      "2.0.0",
		Export:       true,
	}
	l.GrpcLogLevel.Init(base.mgr)
}
